// Package aggregator provides an AlphaEngine that combines multiple
// AlphaStrategy scores using per-strategy weights, then returns a
// descending-sorted []core.Signal across all quoted symbols.
//
// Aggregation formula (weight-normalised):
//
//	totalScore = Σ( clamp(stratᵢ.Score(q), −1, +1) × weightᵢ ) / Σ weightᵢ
package aggregator

import (
	"math"
	"sort"

	"astock_trade/core"
)

type crossSectionalAlpha interface {
	CrossSectionalScores(quotes map[string]*core.Quote) map[string]float64
}

// WeightedAlpha pairs an AlphaStrategy with its relative importance weight.
// Weights need not sum to 1; the engine normalises internally.
type WeightedAlpha struct {
	Alpha  core.AlphaStrategy
	Weight float64 // relative weight, e.g. 0.5 = half the influence of weight 1.0
}

// Engine satisfies core.AlphaEngine.
type Engine struct {
	alphas []WeightedAlpha
}

// New returns an Engine. Panics if no alphas are provided.
func New(alphas ...WeightedAlpha) *Engine {
	if len(alphas) == 0 {
		panic("aggregator.New: at least one WeightedAlpha required")
	}
	return &Engine{alphas: alphas}
}

// Rank scores every symbol present in quotes, computes the weight-normalised
// aggregate, populates Breakdown with per-strategy raw scores, and returns
// the result sorted descending by Score (most bullish first).
func (e *Engine) Rank(quotes map[string]*core.Quote) []core.Signal {
	signals := make([]core.Signal, 0, len(quotes))
	crossScores := make([]map[string]float64, len(e.alphas))
	for i, wa := range e.alphas {
		if alpha, ok := wa.Alpha.(crossSectionalAlpha); ok {
			crossScores[i] = alpha.CrossSectionalScores(quotes)
		}
	}

	for sym, q := range quotes {
		breakdown := make(map[string]float64, len(e.alphas))
		weighted := 0.0
		totalWeight := 0.0

		for i, wa := range e.alphas {
			raw := 0.0
			if scores := crossScores[i]; scores != nil {
				raw = scores[sym]
			} else {
				raw = wa.Alpha.Score(q)
			}
			clamped := math.Max(-1, math.Min(1, raw))
			breakdown[wa.Alpha.Name()] = clamped
			weighted += clamped * wa.Weight
			totalWeight += wa.Weight
		}

		score := 0.0
		if totalWeight > 0 {
			score = weighted / totalWeight
		}

		signals = append(signals, core.Signal{
			Symbol:    sym,
			Score:     score,
			Breakdown: breakdown,
			Timestamp: q.Timestamp,
		})
	}

	// Descending: highest score (most bullish) first
	sort.Slice(signals, func(i, j int) bool {
		return signals[i].Score > signals[j].Score
	})

	return signals
}
