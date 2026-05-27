// Package matrend provides an AlphaStrategy based on the deviation of the
// current price from the EMA-20 supplied directly by the DataProvider.
//
// Root-cause fix: the previous implementation maintained its own internal EMA
// map, which returned 0.0 on every tick because the EMA was seeded only once
// and the random-walk prices diverged immediately.  By reading q.EMA20 (which
// the DataProvider computes from a properly pre-warmed price history) the
// strategy now produces meaningful non-zero scores from tick 1.
//
// NOTE: ma_trend is intentionally kept as a standalone package so it can be
// composed into aggregators when needed.  The canonical demo wiring uses
// volatility instead (see main.go) to avoid factor duplication with momentum.
package matrend

import (
	"math"

	"astock_trade/core"
)

// Config holds tunable parameters.
type Config struct {
	// GainThreshold is the price deviation from EMA20 (as a fraction) that
	// maps to a score of ±1.  Example: 0.03 = 3 % above EMA → score +1.
	GainThreshold float64
}

// Strategy satisfies core.AlphaStrategy.
// Zero internal state — all inputs come from q.Price and q.EMA20.
type Strategy struct {
	cfg Config
}

// New returns a Strategy.  GainThreshold defaults to 0.03 if not set.
func New(cfg Config) *Strategy {
	if cfg.GainThreshold <= 0 {
		cfg.GainThreshold = 0.03
	}
	return &Strategy{cfg: cfg}
}

func (s *Strategy) Name() string { return "ma_trend" }

// Score returns how far the current price sits above/below the EMA-20.
// Returns 0 when q.EMA20 is not yet available (zero value).
func (s *Strategy) Score(q *core.Quote) float64 {
	if q.EMA20 <= 0 {
		return 0
	}
	deviation := (q.Price - q.EMA20) / q.EMA20
	raw := deviation / s.cfg.GainThreshold
	return math.Max(-1, math.Min(1, raw))
}
