// cmd/fetchdata downloads A-share daily historical K-line data from East Money
// and writes it to a CSV file compatible with provider/replay.
//
// Usage:
//
//	go run ./cmd/fetchdata/                                    # defaults
//	go run ./cmd/fetchdata/ -symbols 600519,000858,300750,000300
//	go run ./cmd/fetchdata/ -days 60 -out mydata.csv
//
// Output format (same as paper_data.csv / real_market_data.csv):
//
//	date,symbol,open,high,low,close,volume
//
// Key design decision:
//
//	Uses http.Transport{Proxy: nil} to bypass ALL proxy settings (both
//	environment-variable proxies and macOS system-level proxies set by VPN
//	clients like Surge / ClashX / V2Ray).  This is the same technique used
//	by provider/eastmoney and is proven to work when Python requests fails.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ── East Money K-line API ─────────────────────────────────────────────────────
//
// GET https://push2his.eastmoney.com/api/qt/stock/kline/get
//
// Required params:
//   secid    "1.600519" (Shanghai) or "0.000858" (Shenzhen)
//   fields1  "f1,f2,f3,f4,f5,f6"
//   fields2  "f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61"
//   klt      101 = daily, 102 = weekly, 103 = monthly
//   fqt      0 = no adjustment (actual market price), 1 = forward-adjusted
//   beg      YYYYMMDD start date
//   end      YYYYMMDD end date
//   lmt      max rows returned
//
// kline row format (comma-separated string):
//   [0] date (YYYY-MM-DD)
//   [1] open      (CNY)
//   [2] close     (CNY)
//   [3] high      (CNY)
//   [4] low       (CNY)
//   [5] volume    (手, 1手=100股)
//   [6] amount    (CNY)
//   [7] amplitude (%)
//   [8] pct_chg   (%)
//   [9] change    (CNY)
//  [10] turnover  (%)

const (
	klineURL = "https://push2his.eastmoney.com/api/qt/stock/kline/get"

	// defaultSymbols matches cmd/paper/main.go: symbols + indexSymbol
	defaultSymbols = "600519,000858,300750,000300"

	defaultDays   = 120
	defaultOutput = "real_market_data.csv"
)

// Known index secid overrides (indices do NOT follow the standard stock prefix rule).
var indexSecid = map[string]string{
	"000300": "1.000300", // 沪深300
	"000001": "1.000001", // 上证综指
	"000016": "1.000016", // 上证50
	"000905": "1.000905", // 中证500
	"000852": "1.000852", // 中证1000
	"399001": "0.399001", // 深证成指
	"399006": "0.399006", // 创业板指
}

var symbolNames = map[string]string{
	"600519": "贵州茅台",
	"000858": "五粮液",
	"300750": "宁德时代",
	"000300": "沪深300",
	"601318": "中国平安",
	"000001": "平安银行",
	"600036": "招商银行",
}

// ── HTTP client ───────────────────────────────────────────────────────────────

// newClient returns an HTTP client that bypasses ALL proxy settings.
// On macOS, VPN clients (Surge, ClashX, etc.) set system-level proxies via
// System Preferences → Network → Proxies.  http.Transport{Proxy: nil} ignores
// them completely, routing traffic directly through the OS network stack where
// VPN split-tunneling correctly sends domestic Chinese traffic to domestic servers.
func newClient() *http.Client {
	return &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:               nil, // bypass env vars AND macOS system proxies
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			DisableCompression:  false,
		},
	}
}

// ── API types ─────────────────────────────────────────────────────────────────

type klineResponse struct {
	RC   int `json:"rc"`
	Data *struct {
		Code   string   `json:"code"`
		Klines []string `json:"klines"`
	} `json:"data"`
}

// ── Fetch ─────────────────────────────────────────────────────────────────────

func toSecID(symbol string) string {
	if sid, ok := indexSecid[symbol]; ok {
		return sid
	}
	if len(symbol) > 0 && (symbol[0] == '6' || symbol[0] == '9') {
		return "1." + symbol
	}
	return "0." + symbol
}

func fetchKlines(client *http.Client, symbol, begDate, endDate string, limit int) ([]string, error) {
	secid := toSecID(symbol)

	req, err := http.NewRequest("GET", klineURL, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("secid", secid)
	q.Set("fields1", "f1,f2,f3,f4,f5,f6")
	q.Set("fields2", "f51,f52,f53,f54,f55,f56,f57,f58,f59,f60,f61")
	q.Set("klt", "101")   // daily
	q.Set("fqt", "0")     // no adjustment (实际市价)
	q.Set("beg", begDate)
	q.Set("end", endDate)
	q.Set("lmt", strconv.Itoa(limit))
	req.URL.RawQuery = q.Encode()

	// Mimic browser request to avoid anti-scraping filters.
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var kr klineResponse
	if err := json.Unmarshal(body, &kr); err != nil {
		return nil, fmt.Errorf("JSON: %w", err)
	}
	if kr.RC != 0 {
		return nil, fmt.Errorf("API rc=%d", kr.RC)
	}
	if kr.Data == nil || len(kr.Data.Klines) == 0 {
		return nil, fmt.Errorf("empty klines (symbol=%s secid=%s)", symbol, secid)
	}
	return kr.Data.Klines, nil
}

// parseKline converts one East Money kline string to CSV fields.
// Returns (date, open, high, low, close, volume) or error.
func parseKline(line string) (date, open, high, low, close_ string, volume int64, err error) {
	parts := strings.SplitN(line, ",", 12)
	if len(parts) < 6 {
		err = fmt.Errorf("too few fields: %d", len(parts))
		return
	}
	date = parts[0][:10] // YYYY-MM-DD
	open = parts[1]
	close_ = parts[2]
	high = parts[3]
	low = parts[4]
	// Volume is in 手 (1手=100股); convert to shares.
	volHand, e := strconv.ParseFloat(parts[5], 64)
	if e != nil {
		err = fmt.Errorf("volume parse: %w", e)
		return
	}
	volume = int64(volHand) * 100

	// Sanity check: price must be positive.
	closeF, e := strconv.ParseFloat(close_, 64)
	if e != nil || closeF <= 0 {
		err = fmt.Errorf("invalid close price: %s", close_)
	}
	return
}

// ── CSV row ───────────────────────────────────────────────────────────────────

type row struct {
	date, symbol, open, high, low, close string
	volume                               int64
}

// ── Main ─────────────────────────────────────────────────────────────────────

func main() {
	log.SetFlags(0) // clean output

	symbolsFlag := flag.String("symbols", defaultSymbols, "comma-separated 6-digit stock codes")
	daysFlag := flag.Int("days", defaultDays, "number of trading days to fetch (approx)")
	outFlag := flag.String("out", defaultOutput, "output CSV file path")
	flag.Parse()

	symbols := strings.Split(*symbolsFlag, ",")
	for i, s := range symbols {
		symbols[i] = strings.TrimSpace(s)
	}

	endDate := time.Now()
	// Approximate: 1 trading day ≈ 1.7 calendar days (accounting for weekends/holidays).
	// Add 40 extra days as buffer.
	startDate := endDate.AddDate(0, 0, -int(float64(*daysFlag)*1.7)-40)
	begStr := startDate.Format("20060102")
	endStr := endDate.Format("20060102")

	log.Printf("════════════════════════════════════════════════")
	log.Printf("  AStock 历史行情下载（东方财富 K 线 API）")
	log.Printf("  数据类型: 日线，不复权（实际市价）")
	log.Printf("  日期范围: %s → %s（约 %d 交易日）", begStr, endStr, *daysFlag)
	log.Printf("  标的数量: %d", len(symbols))
	log.Printf("  输出文件: %s", *outFlag)
	log.Printf("  代理设置: 已禁用（直连东方财富）")
	log.Printf("════════════════════════════════════════════════")

	client := newClient()
	var allRows []row
	var failed []string

	for _, sym := range symbols {
		name := symbolNames[sym]
		if name == "" {
			name = sym
		}
		log.Printf("  [%s] %s ... ", sym, name)

		klines, err := fetchKlines(client, sym, begStr, endStr, *daysFlag+50)
		if err != nil {
			log.Printf("    ⚠️  失败: %v", err)
			failed = append(failed, sym)
			continue
		}

		count := 0
		for _, kline := range klines {
			date, open, high, low, close_, volume, e := parseKline(kline)
			if e != nil {
				continue
			}
			allRows = append(allRows, row{
				date: date, symbol: sym,
				open: open, high: high, low: low, close: close_,
				volume: volume,
			})
			count++
		}
		log.Printf("    ✓ %d 条", count)
	}

	if len(allRows) == 0 {
		log.Fatalf("\n错误：所有标的均获取失败。\n" +
			"如仍有问题，请尝试：\n" +
			"  1. 确认东方财富可访问: curl https://push2his.eastmoney.com\n" +
			"  2. 临时关闭 VPN 后重试\n" +
			"  3. 改用实时模式: export ASTOCK_LIVE_DATA=1 && bash scripts/start.sh")
	}

	// Sort by date then symbol for deterministic output.
	sort.Slice(allRows, func(i, j int) bool {
		if allRows[i].date != allRows[j].date {
			return allRows[i].date < allRows[j].date
		}
		return allRows[i].symbol < allRows[j].symbol
	})

	// Write CSV.
	f, err := os.Create(*outFlag)
	if err != nil {
		log.Fatalf("创建文件失败: %v", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	_ = w.Write([]string{"date", "symbol", "open", "high", "low", "close", "volume"})
	for _, r := range allRows {
		_ = w.Write([]string{
			r.date, r.symbol, r.open, r.high, r.low, r.close,
			strconv.FormatInt(r.volume, 10),
		})
	}
	w.Flush()

	log.Printf("")
	log.Printf("════════════════════════════════════════════════")
	log.Printf("  ✅ 下载完成：共 %d 条记录 → %s", len(allRows), *outFlag)
	if len(failed) > 0 {
		log.Printf("  ⚠️  以下标的失败（系统将降级使用合成数据）: %s", strings.Join(failed, ", "))
	}
	log.Printf("════════════════════════════════════════════════")
	log.Printf("")
	log.Printf("  提示: 启动时系统自动加载此文件（优先于合成数据）。")
	log.Printf("  更新数据: go run ./cmd/fetchdata/")
}
