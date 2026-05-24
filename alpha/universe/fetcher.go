package universe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"time"
)

const (
	clistURL     = "https://push2delay.eastmoney.com/api/qt/clist/get"
	aShareFilter = "m:0+t:6,m:0+t:13,m:0+t:80,m:1+t:2,m:1+t:23"
	clistFields  = "f2,f3,f5,f6,f8,f9,f10,f12,f13,f14,f20,f21,f23,f109,f110,f160"
	pageSize     = 100
	naThreshold  = -1_000_000.0
)

type Fetcher struct {
	client *http.Client
}

type StockInfo struct {
	Symbol      string
	Name        string
	Market      int
	Price       float64
	ChangeP     float64
	Volume      int64
	Amount      float64
	Turnover    float64
	PE          float64
	VolumeRatio float64
	MktCap      float64
	FloatCap    float64
	PB          float64
	Ret5d       float64
	Ret10d      float64
	Ret20d      float64
}

type clistResponse struct {
	RC   int `json:"rc"`
	Data struct {
		Total int               `json:"total"`
		Diff  []json.RawMessage `json:"diff"`
	} `json:"data"`
}

type rawStock struct {
	F2   json.Number `json:"f2"`
	F3   json.Number `json:"f3"`
	F5   json.Number `json:"f5"`
	F6   json.Number `json:"f6"`
	F8   json.Number `json:"f8"`
	F9   json.Number `json:"f9"`
	F10  json.Number `json:"f10"`
	F12  string      `json:"f12"`
	F13  json.Number `json:"f13"`
	F14  string      `json:"f14"`
	F20  json.Number `json:"f20"`
	F21  json.Number `json:"f21"`
	F23  json.Number `json:"f23"`
	F109 json.Number `json:"f109"`
	F110 json.Number `json:"f110"`
	F160 json.Number `json:"f160"`
}

func NewFetcher() *Fetcher {
	dialer := &net.Dialer{Timeout: 15 * time.Second}

	return &Fetcher{
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					return dialer.DialContext(ctx, "tcp4", address)
				},
			},
		},
	}
}

func (f *Fetcher) FetchAll(ctx context.Context) ([]StockInfo, error) {
	var out []StockInfo

	for page := 1; ; page++ {
		stocks, total, err := f.fetchPage(ctx, page)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}
		out = append(out, stocks...)
		if len(out) >= total || len(stocks) == 0 {
			break
		}
	}

	return out, nil
}

func (f *Fetcher) fetchPage(ctx context.Context, page int) ([]StockInfo, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clistURL, nil)
	if err != nil {
		return nil, 0, err
	}

	q := req.URL.Query()
	q.Set("pn", fmt.Sprintf("%d", page))
	q.Set("pz", fmt.Sprintf("%d", pageSize))
	q.Set("po", "1")
	q.Set("np", "1")
	q.Set("ut", "bd1d9ddb04089700cf9c27f6f7426281")
	q.Set("fltt", "2")
	q.Set("invt", "2")
	q.Set("fid", "f3")
	q.Set("fs", aShareFilter)
	q.Set("fields", clistFields)
	req.URL.RawQuery = q.Encode()

	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("eastmoney clist status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	var parsed clistResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, err
	}

	stocks := make([]StockInfo, 0, len(parsed.Data.Diff))
	for _, row := range parsed.Data.Diff {
		var rs rawStock
		if err := json.Unmarshal(row, &rs); err != nil {
			continue
		}
		if stock, ok := stockInfoFromRaw(rs); ok {
			stocks = append(stocks, stock)
		}
	}

	return stocks, parsed.Data.Total, nil
}

func stockInfoFromRaw(rs rawStock) (StockInfo, bool) {
	if len(rs.F12) != 6 {
		return StockInfo{}, false
	}

	price := toFloat(rs.F2)
	if price <= 0 {
		return StockInfo{}, false
	}

	return StockInfo{
		Symbol:      rs.F12,
		Name:        rs.F14,
		Market:      int(toFloat(rs.F13)),
		Price:       price,
		ChangeP:     toFloat(rs.F3),
		Volume:      int64(toFloat(rs.F5)) * 100,
		Amount:      toFloat(rs.F6),
		Turnover:    toFloat(rs.F8),
		PE:          toFloat(rs.F9),
		VolumeRatio: toFloat(rs.F10),
		MktCap:      toFloat(rs.F20),
		FloatCap:    toFloat(rs.F21),
		PB:          toFloat(rs.F23),
		Ret5d:       toReturn(rs.F109),
		Ret10d:      toReturn(rs.F110),
		Ret20d:      toReturn(rs.F160),
	}, true
}

func toFloat(n json.Number) float64 {
	if n == "" || n == "-" {
		return 0
	}

	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < naThreshold {
		return 0
	}

	return f
}

func toReturn(n json.Number) float64 {
	value := toFloat(n)
	if value < -100 || value > 1000 {
		return 0
	}
	return value
}
