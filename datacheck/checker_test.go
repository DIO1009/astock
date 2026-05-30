package datacheck

import (
	"testing"
	"time"

	"astock_trade/core"
)

func TestCheck_IndexMissingDoesNotBlockOpen(t *testing.T) {
	c := New(Config{IndexSymbol: "000001.SH"})
	now := time.Now()
	quotes := map[string]*core.Quote{
		"600519": {
			Symbol:    "600519",
			Price:     100,
			Volume:    1000,
			PctChg:    1.2,
			Return5d:  2.3,
			Return20d: 3.4,
			EMA20:     99,
			Volatility: 1.5,
			Timestamp: now.UnixMilli(),
		},
		"600036": {
			Symbol:    "600036",
			Price:     50,
			Volume:    2000,
			PctChg:    -0.5,
			Return5d:  1.1,
			Return20d: 2.2,
			EMA20:     51,
			Volatility: 0.8,
			Timestamp: now.UnixMilli(),
		},
		"601318": {
			Symbol:    "601318",
			Price:     30,
			Volume:    3000,
			PctChg:    0.2,
			Return5d:  -1.0,
			Return20d: 0.5,
			EMA20:     29,
			Volatility: 1.1,
			Timestamp: now.UnixMilli(),
		},
	}

	result := c.Check(quotes, nil)
	if !result.OK {
		t.Fatalf("expected OK=true when index missing, got errors=%v", result.Errors)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("expected no errors, got %v", result.Errors)
	}
}
