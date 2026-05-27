// Package momentum provides a Strategy skeleton based on price-change and
// volume thresholds. Plug in real signal logic (MA crossover, MACD, RSI, etc.)
// without touching any other package.
package momentum

import "astock_trade/core"

// Config holds the tunable parameters exposed to the caller.
type Config struct {
	// MinPctChg is the minimum intraday price-change percentage required.
	MinPctChg float64
	// MinVolume is the minimum traded volume required.
	MinVolume int64
}

// Strategy satisfies core.Strategy.
type Strategy struct {
	cfg Config
}

// New returns a Strategy with the provided configuration.
func New(cfg Config) *Strategy {
	return &Strategy{cfg: cfg}
}

// ShouldBuy returns true when the quote clears the minimum momentum thresholds.
// TODO: enrich with multi-factor signal (e.g. MA crossover, relative strength).
func (s *Strategy) ShouldBuy(q *core.Quote) bool {
	if q.PctChg < s.cfg.MinPctChg {
		return false
	}
	if q.Volume < s.cfg.MinVolume {
		return false
	}
	return true
}
