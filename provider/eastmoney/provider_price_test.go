package eastmoney

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"astock_trade/core"
)

func fptr(v float64) *float64 { return &v }

func almostEqual(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-9
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func emJSON(symbol string, priceCents int) *http.Response {
	_ = symbol
	price := strconv.Itoa(priceCents)
	body := `{"rc":0,"data":{"f43":` + price + `,"f60":` + price + `,"f17":` + price + `,"f19":` + price + `,"f47":1000,"f170":0}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestNormalizeEMPriceCentFields(t *testing.T) {
	cases := []struct {
		name string
		raw  *float64
		want float64
	}{
		{name: "603407 f43", raw: fptr(6323), want: 63.23},
		{name: "603738 f43", raw: fptr(5024), want: 50.24},
		{name: "previous close", raw: fptr(6300), want: 63.00},
		{name: "high price cent value", raw: fptr(10561), want: 105.61},
		{name: "zero", raw: fptr(0), want: 0},
		{name: "negative one", raw: fptr(-1), want: 0},
		{name: "large negative", raw: fptr(-1000000), want: 0},
		{name: "nil", raw: nil, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeEMPrice(tc.raw)
			if !almostEqual(got, tc.want) {
				t.Fatalf("normalizeEMPrice() = %.12f, want %.12f", got, tc.want)
			}
		})
	}
}

func TestNormalizeEMPctChgParsesCentPercentFields(t *testing.T) {
	cases := []struct {
		name      string
		raw       *float64
		price     float64
		prevClose float64
		want      float64
	}{
		{name: "f170 999", raw: fptr(999), want: 9.99},
		{name: "f170 1000", raw: fptr(1000), want: 10.00},
		{name: "f170 -180", raw: fptr(-180), want: -1.80},
		{name: "f170 -912", raw: fptr(-912), want: -9.12},
		{name: "nil f170 falls back to price and prev close", raw: nil, price: 63.23, prevClose: 63.00, want: (63.23 - 63.00) / 63.00 * 100},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeEMPctChg(tc.raw, tc.price, tc.prevClose)
			if !almostEqual(got, tc.want) {
				t.Fatalf("normalizeEMPctChg() = %.12f, want %.12f", got, tc.want)
			}
		})
	}
}

func TestToSecIDDisambiguatesShanghaiCompositeIndex(t *testing.T) {
	cases := []struct {
		symbol string
		want   string
	}{
		{symbol: "000001.SH", want: "1.000001"},
		{symbol: "SH000001", want: "1.000001"},
		{symbol: "000001", want: "0.000001"},
		{symbol: "603407", want: "1.603407"},
		{symbol: "002428", want: "0.002428"},
	}

	for _, tc := range cases {
		t.Run(tc.symbol, func(t *testing.T) {
			if got := toSecID(tc.symbol); got != tc.want {
				t.Fatalf("toSecID(%q) = %q, want %q", tc.symbol, got, tc.want)
			}
		})
	}
}

func TestBuildQuoteFromRawNormalizesAllPriceFields(t *testing.T) {
	st := &symState{}
	raw := &emData{
		F43:  fptr(6323),
		F44:  fptr(6400),
		F45:  fptr(6210),
		F60:  fptr(6300),
		F17:  fptr(6322),
		F19:  fptr(6324),
		F47:  fptr(12345),
		F170: fptr(0),
		F165: fptr(9999),
	}

	q, err := buildQuoteFromRaw("603407", st, raw, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("buildQuoteFromRaw() error = %v", err)
	}

	wantPctChg := (63.23 - 63.00) / 63.00 * 100
	if !almostEqual(q.Price, 63.23) {
		t.Fatalf("Price = %.12f, want 63.23", q.Price)
	}
	if !almostEqual(q.PrevClose, 63.00) {
		t.Fatalf("PrevClose = %.12f, want 63.00", q.PrevClose)
	}
	if !almostEqual(q.Bid1, 63.22) {
		t.Fatalf("Bid1 = %.12f, want 63.22", q.Bid1)
	}
	if !almostEqual(q.Ask1, 63.24) {
		t.Fatalf("Ask1 = %.12f, want 63.24", q.Ask1)
	}
	if q.Volume != 12345 {
		t.Fatalf("Volume = %d, want 12345", q.Volume)
	}
	if !almostEqual(q.PctChg, wantPctChg) {
		t.Fatalf("PctChg = %.12f, want %.12f", q.PctChg, wantPctChg)
	}
	if st.lastQuote != q {
		t.Fatalf("lastQuote was not updated to returned quote")
	}
	if !almostEqual(st.lastRawClose, 63.23) {
		t.Fatalf("lastRawClose = %.12f, want 63.23", st.lastRawClose)
	}
	if !almostEqual(st.lastPrice, 63.23) {
		t.Fatalf("lastPrice = %.12f, want 63.23", st.lastPrice)
	}
	if !almostEqual(q.Return20d, 0) {
		t.Fatalf("Return20d = %.12f, want 0", q.Return20d)
	}
}

func TestBuildQuoteFromRawNormalizesHighPriceWithoutThresholdDependency(t *testing.T) {
	st := &symState{}
	raw := &emData{
		F43: fptr(10561),
		F60: fptr(10100),
		F17: fptr(10560),
		F19: fptr(10562),
		F47: fptr(1),
	}

	q, err := buildQuoteFromRaw("603407", st, raw, time.Unix(1700000000, 0))
	if err != nil {
		t.Fatalf("buildQuoteFromRaw() error = %v", err)
	}

	if !almostEqual(q.Price, 105.61) {
		t.Fatalf("Price = %.12f, want 105.61", q.Price)
	}
	if !almostEqual(q.PrevClose, 101.00) {
		t.Fatalf("PrevClose = %.12f, want 101.00", q.PrevClose)
	}
	if !almostEqual(q.Bid1, 105.60) {
		t.Fatalf("Bid1 = %.12f, want 105.60", q.Bid1)
	}
	if !almostEqual(q.Ask1, 105.62) {
		t.Fatalf("Ask1 = %.12f, want 105.62", q.Ask1)
	}
}

func TestBuildQuoteFromRawRejectsImplausibleJumpAndKeepsCachedQuote(t *testing.T) {
	st := &symState{
		lastQuote: &core.Quote{Symbol: "603407", Price: 63.23, PrevClose: 63.00},
		lastPrice: 63.23,
	}
	raw := &emData{
		F43: fptr(632300),
		F60: fptr(6300),
		F17: fptr(632300),
		F19: fptr(632300),
		F47: fptr(1),
	}

	q, err := buildQuoteFromRaw("603407", st, raw, time.Unix(1700000001, 0))
	if err == nil {
		t.Fatalf("buildQuoteFromRaw() error = nil, want non-nil")
	}
	if q != nil {
		t.Fatalf("buildQuoteFromRaw() quote = %#v, want nil", q)
	}
	if !almostEqual(st.lastQuote.Price, 63.23) {
		t.Fatalf("lastQuote.Price = %.12f, want 63.23", st.lastQuote.Price)
	}
	if !almostEqual(st.lastPrice, 63.23) {
		t.Fatalf("lastPrice = %.12f, want 63.23", st.lastPrice)
	}
}

func TestBuildQuoteFromRawReplacesStaleCorruptedCacheWithNormalizedQuote(t *testing.T) {
	stale := &core.Quote{Symbol: "603407", Price: 6323}
	st := &symState{
		lastQuote: stale,
		lastPrice: 6323,
	}
	raw := &emData{
		F43: fptr(6323),
		F60: fptr(6300),
		F17: fptr(6322),
		F19: fptr(6324),
		F47: fptr(1),
	}

	q, err := buildQuoteFromRaw("603407", st, raw, time.Unix(1700000002, 0))
	if err != nil {
		t.Fatalf("buildQuoteFromRaw() error = %v", err)
	}
	if !almostEqual(q.Price, 63.23) {
		t.Fatalf("Price = %.12f, want 63.23", q.Price)
	}
	if !almostEqual(q.PrevClose, 63.00) {
		t.Fatalf("PrevClose = %.12f, want 63.00", q.PrevClose)
	}
	if st.lastQuote != q {
		t.Fatalf("lastQuote was not updated to returned quote")
	}
	if !almostEqual(st.lastQuote.Price, 63.23) {
		t.Fatalf("lastQuote.Price = %.12f, want 63.23", st.lastQuote.Price)
	}
	if !almostEqual(st.lastPrice, 63.23) {
		t.Fatalf("lastPrice = %.12f, want 63.23", st.lastPrice)
	}
}

func TestRealtimePriceGuardAllowsNormalLimitMoves(t *testing.T) {
	if isImplausibleRealtimePrice(69.30, 63.00) {
		t.Fatalf("10%% move should be allowed")
	}
	if isImplausibleRealtimePrice(50.40, 63.00) {
		t.Fatalf("20%% move should be allowed")
	}
	if !isImplausibleRealtimePrice(6323.00, 63.00) {
		t.Fatalf("6323.00 against 63.00 should be implausible")
	}
	if isImplausibleRealtimePrice(0, 63.00) {
		t.Fatalf("zero price should not be rejected by implausible-jump guard")
	}
	if isImplausibleRealtimePrice(-1, 63.00) {
		t.Fatalf("negative price should not be rejected by implausible-jump guard")
	}
	if isImplausibleRealtimePrice(63.00, 0) {
		t.Fatalf("zero reference should not be rejected by implausible-jump guard")
	}
	if isImplausibleRealtimePrice(63.00, -1) {
		t.Fatalf("negative reference should not be rejected by implausible-jump guard")
	}
}

func TestGetRealtimeConcurrencyLimitCapsTop100Burst(t *testing.T) {
	symbols := make([]string, 32)
	for i := range symbols {
		symbols[i] = "6000" + strconv.Itoa(i+100)[1:]
	}

	p := New()
	var mu sync.Mutex
	active := 0
	maxActive := 0
	p.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()

		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		active--
		mu.Unlock()
		return emJSON("", 10000), nil
	})}

	quotes := p.GetRealtime(symbols)
	if len(quotes) != len(symbols) {
		t.Fatalf("len(quotes) = %d, want %d", len(quotes), len(symbols))
	}
	if maxActive > maxRealtimeConcurrency {
		t.Fatalf("maxActive = %d exceeds maxRealtimeConcurrency = %d", maxActive, maxRealtimeConcurrency)
	}
}

func TestFetchRawWithRetryRetriesTransientEOFOnce(t *testing.T) {
	p := New()
	attempts := 0
	p.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, io.EOF
		}
		return emJSON("600000", 10000), nil
	})}

	q, err := p.getOne("600000")
	if err != nil {
		t.Fatalf("getOne() error = %v", err)
	}
	if q == nil {
		t.Fatalf("getOne() quote = nil, want non-nil")
	}
	if !almostEqual(q.Price, 100.00) {
		t.Fatalf("Price = %.12f, want 100.00", q.Price)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestGetOneUsesRecentCacheFallbackButRejectsStaleCache(t *testing.T) {
	p := New()
	p.mu.Lock()
	p.states["600000"] = &symState{
		lastQuote:   &core.Quote{Symbol: "600000", Price: 100},
		lastFetched: time.Now().Add(-(cacheMaxAge + time.Second)),
	}
	p.states["600001"] = &symState{
		lastQuote:   &core.Quote{Symbol: "600001", Price: 101},
		lastFetched: time.Now().Add(-(cacheFallbackMaxAge + time.Second)),
	}
	p.mu.Unlock()
	p.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, io.EOF
	})}

	q, err := p.getOne("600000")
	if err != nil {
		t.Fatalf("getOne(600000) error = %v", err)
	}
	if q == nil {
		t.Fatalf("getOne(600000) quote = nil, want cached quote")
	}
	if !almostEqual(q.Price, 100) {
		t.Fatalf("cached Price = %.12f, want 100", q.Price)
	}

	q, err = p.getOne("600001")
	if err == nil {
		t.Fatalf("getOne(600001) error = nil, want non-nil")
	}
	if q != nil {
		t.Fatalf("getOne(600001) quote = %#v, want nil", q)
	}
}

func TestIsTransientRealtimeErrorClassifiesNetworkFailures(t *testing.T) {
	if !isTransientRealtimeError(io.EOF) {
		t.Fatalf("io.EOF should be transient")
	}
	if !isTransientRealtimeError(errors.New("read: connection reset by peer")) {
		t.Fatalf("connection reset by peer should be transient")
	}
	if !isTransientRealtimeError(timeoutErr{}) {
		t.Fatalf("timeout net.Error should be transient")
	}
	if isTransientRealtimeError(errors.New("eastmoney rc=-1")) {
		t.Fatalf("eastmoney rc=-1 should not be transient")
	}
}

func TestFetchRawWithRetryStopsOnContextCancellation(t *testing.T) {
	p := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, io.EOF
	})}

	if _, err := p.fetchRawWithRetry(ctx, "600000"); err == nil {
		t.Fatalf("fetchRawWithRetry() error = nil, want non-nil")
	}
}

func TestProviderSourceDoesNotContainOldPriceBugPatterns(t *testing.T) {
	src, err := os.ReadFile("provider.go")
	if err != nil {
		t.Fatalf("ReadFile(provider.go) error = %v", err)
	}

	compact := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "").Replace(string(src))
	if strings.Contains(compact, ">10000") {
		t.Fatalf("provider.go contains old high-value threshold normalization pattern >10000")
	}
	if strings.Contains(compact, "pctChg>1000") || strings.Contains(compact, "pctChg<-1000") {
		t.Fatalf("provider.go contains old f170 threshold normalization pattern around +/-1000")
	}
	if strings.Contains(string(src), "raw.F165") {
		t.Fatalf("provider.go must not use raw.F165 to populate Return20d or other quote fields")
	}
}
