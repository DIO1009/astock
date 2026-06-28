package universe

import (
	"math"
	"testing"
)

// stockPE returns a minimal valid StockInfo with the given PE, symbol, and price.
func stockPE(symbol string, price float64, pe float64, ret5d, ret20d, volRatio, turnover float64) StockInfo {
	return StockInfo{
		Symbol:      symbol,
		Name:        "测试个股",
		Price:       price,
		MktCap:      10_000_000_000, // 100 亿
		PE:          pe,
		VolumeRatio: volRatio,
		Turnover:    turnover,
		Ret5d:       ret5d,
		Ret20d:      ret20d,
	}
}

func TestLayer1FilterExcludesZeroPE(t *testing.T) {
	stocks := []StockInfo{
		stockPE("600001", 10.0, 0, 2.0, 3.0, 1.0, 1.0),
		stockPE("600002", 10.0, 15.0, 2.0, 3.0, 1.0, 1.0),
	}
	result := layer1Filter(stocks, FilterOpts{})
	if len(result) != 1 {
		t.Fatalf("expected 1 stock after PE≤0 filter, got %d", len(result))
	}
	if result[0].Symbol != "600002" {
		t.Fatalf("expected 600002 (PE=15) to pass, got %s", result[0].Symbol)
	}
}

func TestLayer1FilterExcludesNegativePE(t *testing.T) {
	stocks := []StockInfo{
		stockPE("600001", 10.0, -5.0, 2.0, 3.0, 1.0, 1.0),
		stockPE("600002", 10.0, 20.0, 2.0, 3.0, 1.0, 1.0),
	}
	result := layer1Filter(stocks, FilterOpts{})
	if len(result) != 1 {
		t.Fatalf("expected 1 stock after negative PE filter, got %d", len(result))
	}
	if result[0].Symbol != "600002" {
		t.Fatalf("expected 600002 to pass, got %s", result[0].Symbol)
	}
}

func TestScoreAllUsesPEFactor(t *testing.T) {
	// Two stocks with identical everything except PE.
	// Stock A: PE=10  → 1/PE=0.10
	// Stock B: PE=20  → 1/PE=0.05
	// With positive PE weight (0.10), A should score higher than B.
	stocks := []StockInfo{
		stockPE("600001", 10.0, 10.0, 2.0, 3.0, 1.0, 1.0),
		stockPE("600002", 10.0, 20.0, 2.0, 3.0, 1.0, 1.0),
	}
	scored := ScoreAll(stocks)
	if len(scored) != 2 {
		t.Fatalf("expected 2 scored stocks, got %d", len(scored))
	}
	// A (PE=10, higher earnings yield) → higher score
	if scored[0].Symbol != "600001" {
		t.Fatalf("expected 600001 (PE=10) to rank first, got %s", scored[0].Symbol)
	}
	if scored[1].Symbol != "600002" {
		t.Fatalf("expected 600002 (PE=20) to rank second, got %s", scored[1].Symbol)
	}
}

func TestScoreAllWeightSumIsOne(t *testing.T) {
	// Verify the absolute factor weight constants sum to 1.0.
	abs := func(v float64) float64 { return math.Abs(v) }
	sum := abs(wRet5d) + abs(wRet20d) + abs(wVolumeRatio) + abs(wTurnover) + abs(wChangeP) + abs(wPE)
	if math.Abs(sum-1.0) > 1e-9 {
		t.Fatalf("abs(factor weights) sum = %.4f, want 1.0", sum)
	}
}

func TestComputeScoresPEZScoreNormalization(t *testing.T) {
	// Three stocks with different PE values.
	// PE=10→EY=0.10, PE=20→EY=0.05, PE=40→EY=0.025
	// Lower PE → higher EY → higher z-score → positive contribution from wPE.
	stocks := []StockInfo{
		stockPE("A", 10.0, 10.0, 1.0, 2.0, 1.0, 1.0),
		stockPE("B", 10.0, 20.0, 1.0, 2.0, 1.0, 1.0),
		stockPE("C", 10.0, 40.0, 1.0, 2.0, 1.0, 1.0),
	}
	scored := ScoreAll(stocks)
	if len(scored) != 3 {
		t.Fatalf("expected 3 scored stocks, got %d", len(scored))
	}
	// A (PE=10) should rank first
	if scored[0].Symbol != "A" {
		t.Fatalf("expected A (PE=10) first, got %s", scored[0].Symbol)
	}
	// C (PE=40) should rank last
	if scored[2].Symbol != "C" {
		t.Fatalf("expected C (PE=40) last, got %s", scored[2].Symbol)
	}
}
