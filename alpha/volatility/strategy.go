// Package volatility provides a cross-sectional risk factor.
//
// The raw input is q.Volatility (% rolling standard deviation). Scores are
// normalized against the full market distribution for the current tick:
//
//	vol_z = (volatility_raw - mean(volatility_raw)) / std(volatility_raw)
//	score = -tanh(vol_z)
//
// Interpretation:
//   - low volatility  -> positive score (stable)
//   - high volatility -> negative score (risky)
package volatility

import (
	"math"

	"astock_trade/core"
)

// Config is kept for compatibility with existing wiring.
type Config struct {
	// MaxVol is only used by the single-quote fallback path.
	MaxVol float64
}

// Strategy satisfies core.AlphaStrategy and exposes cross-sectional scoring.
type Strategy struct {
	cfg Config
}

// New returns a Strategy. MaxVol defaults to 3.0 for fallback behavior.
func New(cfg Config) *Strategy {
	if cfg.MaxVol <= 0 {
		cfg.MaxVol = 3.0
	}
	return &Strategy{cfg: cfg}
}

func (s *Strategy) Name() string { return "volatility" }

// Score is a fallback for callers that do not provide the market cross-section.
func (s *Strategy) Score(q *core.Quote) float64 {
	if q == nil || q.Volatility <= 0 {
		return 0
	}
	scale := s.cfg.MaxVol
	if scale <= 0 {
		scale = 3.0
	}
	return -math.Tanh((q.Volatility - scale) / scale)
}

// CrossSectionalScores scores every symbol against the full-market distribution.
func (s *Strategy) CrossSectionalScores(quotes map[string]*core.Quote) map[string]float64 {
	scores := make(map[string]float64, len(quotes))
	raws := make(map[string]float64, len(quotes))
	values := make([]float64, 0, len(quotes))

	for sym, q := range quotes {
		if q == nil || q.Volatility <= 0 || math.IsNaN(q.Volatility) || math.IsInf(q.Volatility, 0) {
			scores[sym] = 0
			continue
		}
		raws[sym] = q.Volatility
		values = append(values, q.Volatility)
	}

	if len(values) == 0 {
		return scores
	}

	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))

	variance := 0.0
	for _, v := range values {
		delta := v - mean
		variance += delta * delta
	}
	std := math.Sqrt(variance / float64(len(values)))
	if std < 1e-9 {
		for sym := range raws {
			scores[sym] = 0
		}
		return scores
	}

	for sym, raw := range raws {
		volZ := (raw - mean) / std
		scores[sym] = -math.Tanh(volZ)
	}
	return scores
}
