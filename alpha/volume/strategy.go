// Package volume provides a smooth relative-volume factor.
//
// The preferred input is q.VolumeRatio = volume_today / avg_volume_5d.
// Score mapping:
//
//	score = tanh((volume_ratio - 1.0) * 1.5)
//
// This avoids hard clipping while keeping the output in [-1, +1].
package volume

import (
	"math"

	"astock_trade/core"
)

// Config holds tunable parameters for the volume strategy.
type Config struct {
	// RefVolume is kept only as a fallback when VolumeRatio is unavailable.
	RefVolume int64
}

// Strategy satisfies core.AlphaStrategy.
type Strategy struct {
	cfg Config
}

// New returns a volume Strategy. RefVolume defaults to 500_000 if not set.
func New(cfg Config) *Strategy {
	if cfg.RefVolume <= 0 {
		cfg.RefVolume = 500_000
	}
	return &Strategy{cfg: cfg}
}

func (s *Strategy) Name() string { return "volume" }

// Score returns a smoothed relative-volume score.
func (s *Strategy) Score(q *core.Quote) float64 {
	if q == nil {
		return 0
	}

	ratio := q.VolumeRatio
	if ratio <= 0 && q.AvgVolume5d > 0 {
		ratio = float64(q.Volume) / q.AvgVolume5d
	}
	if ratio <= 0 && s.cfg.RefVolume > 0 && q.Volume > 0 {
		ratio = float64(q.Volume) / float64(s.cfg.RefVolume)
	}
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return 0
	}

	return math.Tanh((ratio - 1.0) * 1.5)
}
