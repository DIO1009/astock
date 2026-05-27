// Package reversal provides a cross-sectional mean-reversion factor.
//
// The signal is based on the market-wide normalized 5-tick return:
//
//	score = -tanh(zscore(return5d))
//
// Positive scores favor recent laggards; negative scores penalize recent
// short-term winners without hard clipping.
package reversal

import (
	"math"

	"astock_trade/core"
)

// Config is kept for compatibility with existing wiring.
type Config struct {
	ThresholdPct float64
	MaxReturn5d  float64
	WeightMA     float64
}

// Strategy satisfies core.AlphaStrategy and exposes cross-sectional scoring.
type Strategy struct {
	cfg Config
}

// New returns a Strategy.
func New(cfg Config) *Strategy {
	if cfg.MaxReturn5d <= 0 {
		cfg.MaxReturn5d = 10.0
	}
	return &Strategy{cfg: cfg}
}

func (s *Strategy) Name() string { return "reversal" }

// Score is a fallback path when cross-sectional data is unavailable.
func (s *Strategy) Score(q *core.Quote) float64 {
	if q == nil {
		return 0
	}
	scale := s.cfg.MaxReturn5d
	if scale <= 0 {
		scale = 10.0
	}
	return -math.Tanh(q.Return5d / scale)
}

// CrossSectionalScores scores return5d against the full-market distribution.
func (s *Strategy) CrossSectionalScores(quotes map[string]*core.Quote) map[string]float64 {
	scores := make(map[string]float64, len(quotes))
	raws := make(map[string]float64, len(quotes))
	values := make([]float64, 0, len(quotes))

	for sym, q := range quotes {
		if q == nil || math.IsNaN(q.Return5d) || math.IsInf(q.Return5d, 0) {
			scores[sym] = 0
			continue
		}
		raws[sym] = q.Return5d
		values = append(values, q.Return5d)
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
		z := (raw - mean) / std
		scores[sym] = -math.Tanh(z)
	}
	return scores
}
