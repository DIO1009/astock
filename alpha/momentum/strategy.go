// Package momentum provides a cross-sectional trend factor.
//
// The primary signal is the market-wide normalized 20-tick return:
//
//	score = tanh(zscore(return20d))
//
// This keeps positive-trend information while avoiding saturation at +1.
package momentum

import (
	"math"

	"astock_trade/core"
)

// Config is kept for wiring compatibility with existing callers.
type Config struct {
	MaxReturn5d  float64
	MaxReturn20d float64
	Weight5d     float64
}

// Strategy satisfies core.AlphaStrategy and exposes cross-sectional scoring.
type Strategy struct {
	cfg Config
}

// New returns a Strategy.
func New(cfg Config) *Strategy {
	if cfg.MaxReturn20d <= 0 {
		cfg.MaxReturn20d = 20.0
	}
	return &Strategy{cfg: cfg}
}

func (s *Strategy) Name() string { return "momentum" }

// Score is a fallback path when cross-sectional data is unavailable.
func (s *Strategy) Score(q *core.Quote) float64 {
	if q == nil {
		return 0
	}
	scale := s.cfg.MaxReturn20d
	if scale <= 0 {
		scale = 20.0
	}
	return math.Tanh(q.Return20d / scale)
}

// CrossSectionalScores scores return20d against the full-market distribution.
func (s *Strategy) CrossSectionalScores(quotes map[string]*core.Quote) map[string]float64 {
	return scoreByZScore(quotes, func(q *core.Quote) float64 { return q.Return20d }, false)
}

func scoreByZScore(quotes map[string]*core.Quote, extractor func(*core.Quote) float64, negate bool) map[string]float64 {
	scores := make(map[string]float64, len(quotes))
	raws := make(map[string]float64, len(quotes))
	values := make([]float64, 0, len(quotes))

	for sym, q := range quotes {
		if q == nil {
			scores[sym] = 0
			continue
		}
		raw := extractor(q)
		if math.IsNaN(raw) || math.IsInf(raw, 0) {
			scores[sym] = 0
			continue
		}
		raws[sym] = raw
		values = append(values, raw)
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
		score := math.Tanh(z)
		if negate {
			score = -score
		}
		scores[sym] = score
	}
	return scores
}
