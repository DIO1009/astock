package dashboard

import (
	"testing"

	"astock_trade/core"
)

func TestOnQuoteRefreshSkipsWhenAllQuotesMissing(t *testing.T) {
	srv := &Server{
		lastSnap: Snapshot{
			Timestamp: 1,
			Positions: []PositionInfo{
				{Symbol: "603989", Quantity: 100, AvgPrice: 36.68, CurrentPrice: 37.50, PnlPct: 2.24},
			},
			Account: AccountInfo{TotalEquity: 99823},
		},
	}

	positions := []core.Position{{Symbol: "603989", Quantity: 100, AvgPrice: 36.68}}
	srv.OnQuoteRefresh(99823, core.PerformanceReport{}, positions, map[string]*core.Quote{})

	if len(srv.lastSnap.Positions) != 1 {
		t.Fatalf("positions len = %d, want 1", len(srv.lastSnap.Positions))
	}
	if srv.lastSnap.Positions[0].CurrentPrice != 37.50 {
		t.Fatalf("CurrentPrice = %.4f, want 37.50 (unchanged)", srv.lastSnap.Positions[0].CurrentPrice)
	}
}

func TestCurrentPriceUsesLastKnownBeforeCostFallback(t *testing.T) {
	p := core.Position{Symbol: "603989", AvgPrice: 36.68}
	last := map[string]float64{"603989": 37.50}

	got := currentPrice(p, map[string]*core.Quote{}, last)
	if got != 37.50 {
		t.Fatalf("currentPrice = %.4f, want 37.50", got)
	}
}

func TestAllHeldQuotesMissing(t *testing.T) {
	positions := []core.Position{{Symbol: "603989", Quantity: 100, AvgPrice: 36.68}}
	if !allHeldQuotesMissing(positions, map[string]*core.Quote{}) {
		t.Fatal("expected all quotes missing")
	}
	if allHeldQuotesMissing(positions, map[string]*core.Quote{
		"603989": {Symbol: "603989", Price: 37.5},
	}) {
		t.Fatal("expected quote present")
	}
}
