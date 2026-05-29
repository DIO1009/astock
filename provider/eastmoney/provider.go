package eastmoney

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"astock_trade/core"
)

const (
	apiURL = "https://push2.eastmoney.com/api/qt/stock/get"
	// f165 is requested only as an observed/compatibility field. It is not used
	// to populate Return20d because it has not been verified as a 20-day return.
	requestFields     = "f43,f44,f45,f47,f60,f170,f17,f19,f165"
	indexRequestFields = "f43,f44,f45,f47,f60,f170"
	histLen           = 21
	emaAlpha          = 2.0 / (20.0 + 1.0)
	cacheMaxAge       = 15 * time.Second

	realtimeFetchAttempts      = 3
	realtimeRetryBaseDelay     = 400 * time.Millisecond
	cacheFallbackMaxAge       = 60 * time.Second
	noDataThreshold           = 3
	maxRealtimePriceMoveRatio = 0.50
)

type emResponse struct {
	RC   int     `json:"rc"`
	Data *emData `json:"data"`
}

type emData struct {
	F43  *float64 `json:"f43"`
	F44  *float64 `json:"f44"`
	F45  *float64 `json:"f45"`
	F47  *float64 `json:"f47"`
	F60  *float64 `json:"f60"`
	F170 *float64 `json:"f170"`
	F17  *float64 `json:"f17"`
	F19  *float64 `json:"f19"`
	F165 *float64 `json:"f165"`
}

type symState struct {
	closes       []float64
	volumes      []int64
	ema20        float64
	lastQuote    *core.Quote
	lastFetched  time.Time
	lastErr      error
	lastNoData   int
	lastPrice    float64
	lastRawClose float64
}

func (st *symState) appendObservation(close float64, volume int64) {
	if close <= 0 {
		return
	}
	st.closes = append(st.closes, close)
	if len(st.closes) > histLen {
		copy(st.closes, st.closes[len(st.closes)-histLen:])
		st.closes = st.closes[:histLen]
	}
	st.volumes = append(st.volumes, volume)
	if len(st.volumes) > histLen {
		copy(st.volumes, st.volumes[len(st.volumes)-histLen:])
		st.volumes = st.volumes[:histLen]
	}
	if st.ema20 == 0 {
		st.ema20 = close
	} else {
		st.ema20 = emaAlpha*close + (1-emaAlpha)*st.ema20
	}
	st.lastPrice = close
}

func (st *symState) closeNDaysAgo(n int) float64 {
	if st == nil || n <= 0 || len(st.closes) <= n {
		return 0
	}
	return st.closes[len(st.closes)-1-n]
}

func (st *symState) avgVolumeNDays(n int) float64 {
	if st == nil || n <= 0 || len(st.volumes) < n {
		return 0
	}
	start := len(st.volumes) - n
	var total int64
	for _, v := range st.volumes[start:] {
		total += v
	}
	return float64(total) / float64(n)
}

func (st *symState) return5d(price float64) float64 {
	base := st.closeNDaysAgo(5)
	if price <= 0 || base <= 0 {
		return 0
	}
	return (price - base) / base * 100
}

func (st *symState) return20d(price float64) float64 {
	base := st.closeNDaysAgo(20)
	if price <= 0 || base <= 0 {
		return 0
	}
	return (price - base) / base * 100
}

func (st *symState) volatility() float64 {
	if st == nil || len(st.closes) < 2 {
		return 0
	}
	returns := make([]float64, 0, len(st.closes)-1)
	for i := 1; i < len(st.closes); i++ {
		prev := st.closes[i-1]
		if prev <= 0 {
			continue
		}
		returns = append(returns, (st.closes[i]-prev)/prev*100)
	}
	if len(returns) == 0 {
		return 0
	}
	var sum float64
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))
	var sq float64
	for _, r := range returns {
		d := r - mean
		sq += d * d
	}
	return math.Sqrt(sq / float64(len(returns)))
}

func resolveReturn20d(st *symState, price float64, _ *emData) float64 {
	if st == nil {
		return 0
	}
	return st.return20d(price)
}

type Provider struct {
	mu             sync.Mutex
	states         map[string]*symState
	client         *http.Client
	httpLimiterMu  sync.Mutex
	lastHTTPAt     time.Time
}

func New() *Provider {
	return &Provider{
		states: make(map[string]*symState),
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   3 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   3 * time.Second,
				ResponseHeaderTimeout: 3 * time.Second,
			},
		},
	}
}

func (p *Provider) GetRealtime(symbols []string) map[string]*core.Quote {
	out := make(map[string]*core.Quote, len(symbols))
	limit := realtimeConcurrencyLimit(len(symbols))
	if limit <= 1 {
		for _, sym := range symbols {
			if sym == "" {
				continue
			}
			q, err := p.getOne(sym)
			if err != nil {
				log.Printf("[EastMoney] 获取 %s 实时行情失败: %v", sym, err)
				continue
			}
			out[sym] = q
		}
		return out
	}

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var outMu sync.Mutex
	for _, sym := range symbols {
		symbol := sym
		if symbol == "" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			q, err := p.getOne(symbol)
			if err != nil {
				log.Printf("[EastMoney] 获取 %s 实时行情失败: %v", symbol, err)
				return
			}
			outMu.Lock()
			out[symbol] = q
			outMu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (p *Provider) getOne(symbol string) (*core.Quote, error) {
	st := p.stateFor(symbol)
	now := time.Now()
	p.mu.Lock()
	if st.lastQuote != nil && now.Sub(st.lastFetched) <= cacheMaxAge {
		q := *st.lastQuote
		p.mu.Unlock()
		return &q, nil
	}
	p.mu.Unlock()

	raw, err := p.fetchRawWithRetry(context.Background(), symbol)
	if err != nil {
		p.mu.Lock()
		st.lastErr = err
		if cached := p.cachedQuoteIfRecent(st, time.Now()); cached != nil {
			p.mu.Unlock()
			return cached, nil
		}
		p.mu.Unlock()
		return nil, err
	}
	if raw == nil || raw.F43 == nil {
		err := fmt.Errorf("eastmoney no data for %s", symbol)
		p.mu.Lock()
		st.lastNoData++
		if st.lastNoData <= noDataThreshold {
			if cached := p.cachedQuoteIfRecent(st, time.Now()); cached != nil {
				p.mu.Unlock()
				return cached, nil
			}
		}
		p.mu.Unlock()
		return nil, err
	}

	if normalizeEMPrice(raw.F43) <= 0 {
		err := fmt.Errorf("eastmoney no data for %s", symbol)
		p.mu.Lock()
		st.lastNoData++
		if st.lastNoData <= noDataThreshold {
			if cached := p.cachedQuoteIfRecent(st, time.Now()); cached != nil {
				p.mu.Unlock()
				return cached, nil
			}
		}
		p.mu.Unlock()
		return nil, err
	}

	now = time.Now()
	p.mu.Lock()
	q, err := buildQuoteFromRaw(symbol, st, raw, now)
	if err != nil {
		st.lastErr = err
		if cached := p.cachedQuoteIfRecent(st, now); cached != nil {
			p.mu.Unlock()
			return cached, nil
		}
		p.mu.Unlock()
		return nil, err
	}
	p.mu.Unlock()
	return q, nil
}

func (p *Provider) DiagnosticInputs(symbols []string) map[string]core.FactorDiagnosticInput {
	out := make(map[string]core.FactorDiagnosticInput, len(symbols))
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, symbol := range symbols {
		st := p.states[symbol]
		if st == nil || st.lastQuote == nil {
			continue
		}
		q := st.lastQuote
		out[symbol] = core.FactorDiagnosticInput{
			Symbol:        symbol,
			Close:         q.Price,
			Close1dAgo:    st.closeNDaysAgo(1),
			Close5dAgo:    st.closeNDaysAgo(5),
			Close20dAgo:   st.closeNDaysAgo(20),
			VolumeToday:   q.Volume,
			AvgVolume5d:   q.AvgVolume5d,
			PctChg:        q.PctChg,
			Return5dRaw:   q.Return5d,
			Return20dRaw:  q.Return20d,
			EMA20:         q.EMA20,
			VolatilityRaw: q.Volatility,
			VolumeRatio:   q.VolumeRatio,
		}
	}
	return out
}

func (p *Provider) fetchRawWithRetry(ctx context.Context, symbol string) (*emData, error) {
	var lastErr error
	for attempt := 0; attempt < realtimeFetchAttempts; attempt++ {
		raw, err := p.fetchRaw(ctx, symbol)
		if err == nil {
			return raw, nil
		}
		lastErr = err
		if attempt+1 >= realtimeFetchAttempts || !isTransientRealtimeError(err) {
			return nil, err
		}
		backoff := realtimeRetryBaseDelay * time.Duration(attempt+1)
		if errors.Is(err, io.EOF) {
			backoff = minInterval
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func isTransientRealtimeError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, part := range []string{"connection reset", "connection refused", "unexpected eof", "server closed idle connection", "timeout"} {
		if strings.Contains(msg, part) {
			return true
		}
	}
	return false
}

func (p *Provider) fetchRaw(ctx context.Context, symbol string) (*emData, error) {
	if p.client == nil {
		p.client = New().client
	}
	p.throttleHTTP()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	fields := requestFields
	if isShanghaiIndexAlias(symbol) {
		fields = indexRequestFields
	}
	q := req.URL.Query()
	q.Set("secid", toSecID(symbol))
	q.Set("fields", fields)
	q.Set("ut", "fa5fd1943c7b386f172d6893dbfba10b")
	req.URL.RawQuery = q.Encode()
	req.Header.Set("User-Agent", "Mozilla/5.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("eastmoney status %d: %s", resp.StatusCode, string(body))
	}
	var decoded emResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	if decoded.RC != 0 {
		return nil, fmt.Errorf("eastmoney rc=%d", decoded.RC)
	}
	return decoded.Data, nil
}

func (p *Provider) throttleHTTP() {
	p.httpLimiterMu.Lock()
	defer p.httpLimiterMu.Unlock()
	if minInterval <= 0 {
		p.lastHTTPAt = time.Now()
		return
	}
	wait := minInterval - time.Since(p.lastHTTPAt)
	if wait > 0 {
		time.Sleep(wait)
	}
	p.lastHTTPAt = time.Now()
}

func realtimeConcurrencyLimit(symbolCount int) int {
	if symbolCount <= 0 {
		return 1
	}
	limit := maxRealtimeConcurrency
	if symbolCount < limit {
		return symbolCount
	}
	return limit
}

func (p *Provider) stateFor(symbol string) *symState {
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
	return st
}

func (p *Provider) cachedQuoteIfRecent(st *symState, now time.Time) *core.Quote {
	if st.lastQuote == nil || now.Sub(st.lastFetched) > cacheFallbackMaxAge {
		return nil
	}
	q := *st.lastQuote
	return &q
}

func isShanghaiIndexAlias(symbol string) bool {
	switch symbol {
	case "000001.SH", "SH000001", "000016.SH", "SH000016", "000300.SH", "SH000300", "000905.SH", "SH000905":
		return true
	default:
		return false
	}
}

func toSecID(symbol string) string {
	switch symbol {
	case "000001.SH", "SH000001":
		return "1.000001"
	case "000016.SH", "SH000016":
		return "1.000016"
	case "000300.SH", "SH000300":
		return "1.000300"
	case "000905.SH", "SH000905":
		return "1.000905"
	}
	if len(symbol) > 0 && (symbol[0] == '6' || symbol[0] == '9') {
		return "1." + symbol
	}
	return "0." + symbol
}

func normalizeEMPrice(v *float64) float64 {
	if v == nil {
		return 0
	}
	raw := *v
	if math.IsNaN(raw) || math.IsInf(raw, 0) || raw <= 0 || raw <= -1_000_000 {
		return 0
	}
	return raw / 100
}

func realtimePriceReferences(st *symState, prevClose float64) []float64 {
	refs := make([]float64, 0, 3)
	add := func(price float64) {
		if price <= 0 {
			return
		}
		for _, ref := range refs {
			if ref == price {
				return
			}
		}
		refs = append(refs, price)
	}
	add(prevClose)
	if st != nil {
		if st.lastQuote != nil {
			add(st.lastQuote.Price)
		}
		add(st.lastPrice)
	}
	return refs
}

func isImplausibleRealtimePrice(price float64, references ...float64) bool {
	if price <= 0 {
		return false
	}
	hasPositiveReference := false
	for _, reference := range references {
		if reference <= 0 {
			continue
		}
		hasPositiveReference = true
		if math.Abs(price-reference)/reference <= maxRealtimePriceMoveRatio {
			return false
		}
	}
	return hasPositiveReference
}

func buildQuoteFromRaw(symbol string, st *symState, raw *emData, now time.Time) (*core.Quote, error) {
	price := normalizeEMPrice(raw.F43)
	if price <= 0 {
		return nil, fmt.Errorf("eastmoney invalid price for %s", symbol)
	}
	high := normalizeEMPrice(raw.F44)
	low := normalizeEMPrice(raw.F45)
	prevClose := normalizeEMPrice(raw.F60)
	bid1 := normalizeEMPrice(raw.F17)
	ask1 := normalizeEMPrice(raw.F19)
	volume := int64(fieldToFloat(raw.F47))
	pctChg := normalizeEMPctChg(raw.F170, price, prevClose)
	if bid1 == 0 {
		bid1 = price
	}
	if ask1 == 0 {
		ask1 = price
	}
	_ = high
	_ = low

	if isImplausibleRealtimePrice(price, realtimePriceReferences(st, prevClose)...) {
		return nil, fmt.Errorf("eastmoney implausible realtime price for %s: %.4f", symbol, price)
	}

	st.appendObservation(price, volume)
	avgVol5d := st.avgVolumeNDays(5)
	volumeRatio := 0.0
	if avgVol5d > 0 {
		volumeRatio = float64(volume) / avgVol5d
	}
	q := &core.Quote{
		Symbol:      symbol,
		Price:       price,
		PrevClose:   prevClose,
		Bid1:        bid1,
		Ask1:        ask1,
		Volume:      volume,
		PctChg:      pctChg,
		Return5d:    st.return5d(price),
		Return20d:   resolveReturn20d(st, price, raw),
		EMA20:       st.ema20,
		Volatility:  st.volatility(),
		AvgVolume5d: avgVol5d,
		VolumeRatio: volumeRatio,
		Timestamp:   now.UnixMilli(),
	}
	st.lastQuote = q
	st.lastFetched = now
	st.lastErr = nil
	st.lastNoData = 0
	st.lastRawClose = price
	return q, nil
}

func fieldToFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func normalizeEMPctChg(v *float64, price float64, prevClose float64) float64 {
	if v != nil {
		raw := *v
		if !math.IsNaN(raw) && !math.IsInf(raw, 0) && raw != 0 {
			return raw / 100
		}
	}
	if price > 0 && prevClose > 0 {
		return (price - prevClose) / prevClose * 100
	}
	return 0
}

func (p *Provider) LastPrice(symbol string) float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.states[symbol]
	if st == nil {
		return 0
	}
	if st.lastQuote != nil {
		return st.lastQuote.Price
	}
	return st.lastPrice
}
