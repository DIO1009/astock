package eastmoney

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tencentKlineURL = "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get"
	prewarmDays     = 21
)

type DailyPoint struct {
	Close  float64
	Volume int64
}

func (p *Provider) PreWarm(symbol string, points []DailyPoint) {
	if len(points) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.states == nil {
		p.states = make(map[string]*symState)
	}
	st := p.states[symbol]
	if st == nil {
		st = &symState{}
		p.states[symbol] = st
	}
	st.closes = nil
	st.volumes = nil
	st.ema20 = 0
	st.lastPrice = 0
	st.lastRawClose = 0
	for _, point := range points {
		st.appendObservation(point.Close, point.Volume)
	}
}

func FetchDailyCloses(ctx context.Context, symbols []string, n int) map[string][]DailyPoint {
	if n <= 0 {
		n = prewarmDays
	}
	out := make(map[string][]DailyPoint, len(symbols))
	var outMu sync.Mutex
	sem := make(chan struct{}, 10)
	var wg sync.WaitGroup
	client := &http.Client{Timeout: 8 * time.Second}

	for _, sym := range symbols {
		symbol := strings.TrimSpace(sym)
		if symbol == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				log.Printf("[PreWarm] %s 历史K线获取失败: %v", symbol, ctx.Err())
				return
			}
			points, err := fetchTencentDailyCloses(ctx, client, symbol, n)
			if err != nil {
				log.Printf("[PreWarm] %s 历史K线获取失败: %v", symbol, err)
				return
			}
			if len(points) == 0 {
				return
			}
			outMu.Lock()
			out[symbol] = points
			outMu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func fetchTencentDailyCloses(ctx context.Context, client *http.Client, symbol string, n int) ([]DailyPoint, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tencentKlineURL, nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	q.Set("param", fmt.Sprintf("%s,day,,,%d,qfq", toTencentSymbol(symbol), n))
	req.URL.RawQuery = q.Encode()
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tencent kline status %d: %s", resp.StatusCode, string(body))
	}
	points, err := parseTencentCloses(body, toTencentSymbol(symbol))
	if err != nil {
		return nil, err
	}
	if len(points) > n {
		points = points[len(points)-n:]
	}
	return points, nil
}

func parseTencentCloses(body []byte, tencentSymbol string) ([]DailyPoint, error) {
	var decoded struct {
		Code int `json:"code"`
		Data map[string]struct {
			Day    [][]interface{} `json:"day"`
			QFQDay [][]interface{} `json:"qfqday"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	if decoded.Code != 0 {
		return nil, fmt.Errorf("tencent kline code=%d", decoded.Code)
	}
	data, ok := decoded.Data[tencentSymbol]
	if !ok {
		return nil, fmt.Errorf("tencent kline missing data for %s", tencentSymbol)
	}
	rows := data.QFQDay
	if len(rows) == 0 {
		rows = data.Day
	}
	points := make([]DailyPoint, 0, len(rows))
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		close, ok := parseTencentFloat(row[2])
		if !ok || close <= 0 {
			continue
		}
		var volume int64
		if len(row) > 5 {
			volume = parseTencentInt(row[5])
		}
		points = append(points, DailyPoint{Close: close, Volume: volume})
	}
	return points, nil
}

func toTencentSymbol(symbol string) string {
	s := strings.TrimSpace(strings.ToLower(symbol))
	if strings.HasPrefix(s, "sh") || strings.HasPrefix(s, "sz") {
		return s
	}
	if strings.HasPrefix(s, "6") || strings.HasPrefix(s, "9") {
		return "sh" + s
	}
	return "sz" + s
}

func parseTencentFloat(v interface{}) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	default:
		return 0, false
	}
}

func parseTencentInt(v interface{}) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0
		}
		return int64(f)
	default:
		return 0
	}
}
