// Package breakout provides a volume-confirmed price-breakout AlphaStrategy.
//
// Core idea: a stock that breaks above its recent range with high volume is
// starting a new momentum leg; the volume confirmation separates real breakouts
// from false ones (noise spikes without participation).
//
// Score formula:
//
//	returnNorm   = clamp(Return5d / BreakoutThreshold, −1, +1)
//	volMultiplier = clamp(Volume / RefVolume, 0.1, 2.0) / 2.0     → [0.05, 1.0]
//	Score        = clamp(returnNorm × volMultiplier × 2, −1, +1)
//
// At full breakout (Return5d = BreakoutThreshold) with 2× normal volume → +1.0.
// At full breakdown (negative return, high volume) → −1.0.
// With average volume, score = returnNorm × 0.5 × 2 = returnNorm.
//
// This strategy complements momentum (which uses both 5d and 20d) and reversal
// (which favours mean-reversion).  It activates specifically when a short burst
// of price movement is backed by significant trading activity.
package breakout

import (
	"math"

	"astock_trade/core"
)

// Config holds tunable parameters.
type Config struct {
	// BreakoutThreshold: 5-tick return (%) at which the score reaches ±1.
	// E.g. 8.0 = an 8 % 5-tick gain → returnNorm = +1.
	BreakoutThreshold float64

	// RefVolume: baseline "normal" volume used to compute the volume multiplier.
	// Volumes at this level produce a multiplier of 0.5; 2× this level → 1.0.
	RefVolume int64
}

// Strategy satisfies core.AlphaStrategy.
// Zero internal state — all inputs come from q.Return5d and q.Volume.
type Strategy struct {
	cfg Config
}

// New returns a Strategy.  Defaults: BreakoutThreshold=8.0, RefVolume=500_000.
func New(cfg Config) *Strategy {
	if cfg.BreakoutThreshold <= 0 {
		cfg.BreakoutThreshold = 8.0
	}
	if cfg.RefVolume <= 0 {
		cfg.RefVolume = 500_000
	}
	return &Strategy{cfg: cfg}
}

func (s *Strategy) Name() string { return "breakout" }

// Score returns how strongly the stock is exhibiting a volume-confirmed breakout.
//
//   - Score near +1: strong upside breakout with above-average volume
//   - Score near  0: flat/moderate move or low volume
//   - Score near −1: strong breakdown with above-average volume (bearish)
func (s *Strategy) Score(q *core.Quote) float64 {
	if q.Volume <= 0 {
		return 0
	}

	// Normalised 5-tick return (direction and magnitude of the move).
	returnNorm := q.Return5d / s.cfg.BreakoutThreshold
	returnNorm = math.Max(-1, math.Min(1, returnNorm))

	// Volume multiplier: high volume validates the breakout; low volume attenuates.
	volRatio := float64(q.Volume) / float64(s.cfg.RefVolume)
	volRatio = math.Max(0.1, math.Min(2.0, volRatio))
	volMultiplier := volRatio / 2.0 // [0.05, 1.0]; average vol → 0.5

	score := returnNorm * volMultiplier * 2 // rescale: average vol → full returnNorm
	return math.Max(-1, math.Min(1, score))
}
