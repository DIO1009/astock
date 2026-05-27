// Package random provides a Strategy that fires a BUY signal with a fixed
// probability. Used exclusively for pipeline validation; not a real signal.
package random

import (
	"math/rand"
	"time"

	"astock_trade/core"
)

// Strategy satisfies core.Strategy.
type Strategy struct {
	rng       *rand.Rand
	threshold float64 // probability of returning true, e.g. 0.10 = 10 %
}

// New returns a Strategy that buys with the given probability [0, 1].
func New(threshold float64) *Strategy {
	return &Strategy{
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
		threshold: threshold,
	}
}

// ShouldBuy returns true with probability equal to threshold.
func (s *Strategy) ShouldBuy(_ *core.Quote) bool {
	return s.rng.Float64() < s.threshold
}
