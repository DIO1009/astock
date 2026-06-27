// Package trailstop provides TRAIL_STOP exit post-mortem analysis ("遗珠"分析).
// For each TRAIL_STOP exit, it fetches daily K-line data and calculates
// the peak price within 5/10/20 trading-day windows after the exit,
// revealing potential missed profit.
package trailstop

import (
	"context"
	"fmt"
	"time"

	"astock_trade/provider/eastmoney"
	"astock_trade/store"
)

// PeakResult holds the analysis result for a single TRAIL_STOP exit.
type PeakResult struct {
	Symbol     string
	ExitDate   string
	ExitPrice  float64
	EntryPrice float64
	Peak5d     float64
	Peak10d    float64
	Peak20d    float64
	Gain5d     float64 // (Peak5d - ExitPrice) / ExitPrice
	Gain10d    float64
	Gain20d    float64
	Skipped    bool
	SkipReason string
}

// Analyzer queries TRAIL_STOP exits and runs peak analysis.
type Analyzer struct {
	store *store.Store
}

// New creates an Analyzer backed by the given Store.
func New(s *store.Store) *Analyzer {
	return &Analyzer{store: s}
}

// Run queries all TRAIL_STOP exits and returns peak analysis results.
// Returns nil, nil when there are no TRAIL_STOP exits.
func (a *Analyzer) Run(ctx context.Context) ([]PeakResult, error) {
	exits, err := a.store.QueryTrailStopExits(ctx)
	if err != nil {
		return nil, fmt.Errorf("query exits: %w", err)
	}
	if len(exits) == 0 {
		return nil, nil
	}

	// Collect unique symbols for batch K-line fetch.
	symbolSet := make(map[string]bool, len(exits))
	for _, e := range exits {
		symbolSet[e.Symbol] = true
	}
	symbols := make([]string, 0, len(symbolSet))
	for s := range symbolSet {
		symbols = append(symbols, s)
	}

	klineData := eastmoney.FetchDailyCloses(ctx, symbols, 21)

	results := make([]PeakResult, 0, len(exits))
	for _, e := range exits {
		r := a.analyzeOne(e, klineData[e.Symbol])
		results = append(results, r)
	}
	return results, nil
}

func (a *Analyzer) analyzeOne(exit store.TrailStopExit, points []eastmoney.DailyPoint) PeakResult {
	exitDate := time.UnixMilli(exit.ExitTS).In(cst).Format("2006-01-02")
	r := PeakResult{
		Symbol:     exit.Symbol,
		ExitDate:   exitDate,
		ExitPrice:  exit.ExitPrice,
		EntryPrice: exit.EntryPrice,
	}

	if len(points) == 0 {
		r.Skipped = true
		r.SkipReason = "K-line data unavailable"
		return r
	}

	// Filter to trading days strictly after the exit date.
	after := make([]eastmoney.DailyPoint, 0, len(points))
	for _, p := range points {
		if p.Date > exitDate {
			after = append(after, p)
		}
	}
	if len(after) == 0 {
		r.Skipped = true
		r.SkipReason = "no trading days after exit in available data"
		return r
	}

	r.Peak5d = maxClose(after, 5)
	r.Peak10d = maxClose(after, 10)
	r.Peak20d = maxClose(after, 20)

	if r.Peak5d > 0 && r.ExitPrice > 0 {
		r.Gain5d = (r.Peak5d - r.ExitPrice) / r.ExitPrice
	}
	if r.Peak10d > 0 && r.ExitPrice > 0 {
		r.Gain10d = (r.Peak10d - r.ExitPrice) / r.ExitPrice
	}
	if r.Peak20d > 0 && r.ExitPrice > 0 {
		r.Gain20d = (r.Peak20d - r.ExitPrice) / r.ExitPrice
	}

	return r
}

// cst is the China Standard Time zone (UTC+8).
var cst = time.FixedZone("CST", 8*3600)

// maxClose returns the highest Close within the first n points.
func maxClose(points []eastmoney.DailyPoint, n int) float64 {
	if len(points) == 0 {
		return 0
	}
	end := n
	if end > len(points) {
		end = len(points)
	}
	m := points[0].Close
	for i := 1; i < end; i++ {
		if points[i].Close > m {
			m = points[i].Close
		}
	}
	return m
}
