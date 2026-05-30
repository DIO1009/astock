package engine

import (
	"testing"

	"astock_trade/core"
)

func TestHasAnyValidPositionQuote(t *testing.T) {
	positions := []core.Position{{Symbol: "603989", Quantity: 100, AvgPrice: 36.68}}

	if hasAnyValidPositionQuote(positions, map[string]*core.Quote{}) {
		t.Fatal("empty quotes should not count as valid")
	}
	if !hasAnyValidPositionQuote(positions, map[string]*core.Quote{
		"603989": {Symbol: "603989", Price: 37.5},
	}) {
		t.Fatal("expected valid quote for held symbol")
	}
}
