package eastmoney

import (
	"os"
	"strings"
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
