// Package dampener provides a SignalAdjuster that prevents a single symbol from
// monopolising the top rank indefinitely.
//
// When a symbol has been ranked #1 for more than MaxTop1Streak consecutive ticks,
// its score is multiplied by DampenFactor (< 1.0), reducing its aggregate score
// so that other stocks get a chance to surface.
//
// Dampening resets automatically once the symbol drops out of the #1 slot.
//
// Thread-safe for concurrent use.
package dampener

import (
	"sort"
	"sync"

	"astock_trade/core"
)

// Config holds tunable parameters.
type Config struct {
	// MaxTop1Streak: number of consecutive ticks at rank #1 before dampening kicks in.
	// Example: 3 → dampen from the 4th consecutive tick onward.
	MaxTop1Streak int

	// DampenFactor: score multiplier applied when the streak is exceeded.
	// Must be in (0, 1).  Example: 0.6 → score × 0.6.
	DampenFactor float64
}

// Dampener satisfies core.SignalAdjuster.
type Dampener struct {
	mu          sync.Mutex
	top1Counts  map[string]int // symbol → consecutive #1 streak
	cfg         Config
}

// New returns a Dampener. Defaults: MaxTop1Streak=3, DampenFactor=0.6.
func New(cfg Config) *Dampener {
	if cfg.MaxTop1Streak <= 0 {
		cfg.MaxTop1Streak = 3
	}
	if cfg.DampenFactor <= 0 || cfg.DampenFactor >= 1 {
		cfg.DampenFactor = 0.6
	}
	return &Dampener{
		top1Counts: make(map[string]int),
		cfg:        cfg,
	}
}

// Adjust updates the consecutive #1 streak counter for the current top-ranked
// symbol, applies score dampening if the streak exceeds MaxTop1Streak, re-sorts
// the signal list, and returns:
//
//   - adjusted: (possibly re-sorted) signal list with dampened scores
//   - dampenedSymbols: map of symbol → current streak count for logging;
//     only contains symbols actively being dampened
func (d *Dampener) Adjust(signals []core.Signal) ([]core.Signal, map[string]int) {
	if len(signals) == 0 {
		return signals, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	currentTop1 := signals[0].Symbol

	// Update streak counters.
	for sym := range d.top1Counts {
		if sym != currentTop1 {
			d.top1Counts[sym] = 0
		}
	}
	d.top1Counts[currentTop1]++

	// Apply dampening where streak exceeds threshold.
	result := make([]core.Signal, len(signals))
	copy(result, signals)

	dampened := make(map[string]int)
	for i, sig := range result {
		streak := d.top1Counts[sig.Symbol]
		if streak > d.cfg.MaxTop1Streak {
			result[i].Score = sig.Score * d.cfg.DampenFactor
			// Record in Breakdown for transparency.
			if result[i].Breakdown == nil {
				result[i].Breakdown = make(map[string]float64)
			}
			result[i].Breakdown["_dampened"] = result[i].Score
			dampened[sig.Symbol] = streak
		}
	}

	// Re-sort descending after score adjustments.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})

	return result, dampened
}
