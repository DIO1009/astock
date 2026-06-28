// Package stability provides a SignalStabilizer that enforces entry discipline.
//
// A symbol must appear in the top-N ranked signals for at least MinConsecutive
// consecutive ticks before it is promoted to a "stable" BUY candidate.
//
// This prevents the system from chasing fleeting one-tick spikes and reduces
// whipsaw entries on noisy signals.
//
// Thread-safe for concurrent use.
package stability

import (
	"sort"
	"sync"

	"astock_trade/core"
)

// Config holds tunable parameters.
type Config struct {
	// MinConsecutive is the number of consecutive ticks a symbol must rank
	// within the top-N before being eligible for a BUY order.
	// Example: 3 → the symbol must be in TopN for three ticks in a row.
	MinConsecutive int

	// TopN defines which rank window counts as "in TopN" for stability tracking.
	// Example: TopN=2 → only the #1 and #2 ranked symbols accumulate counts.
	TopN int
}

// Stabilizer satisfies core.SignalStabilizer.
type Stabilizer struct {
	mu          sync.Mutex
	counts      map[string]int // symbol → current consecutive-in-TopN count
	cfg         Config
	marketState core.MarketState
}

// New returns a Stabilizer. Defaults: MinConsecutive=3, TopN=3.
func New(cfg Config) *Stabilizer {
	if cfg.MinConsecutive <= 0 {
		cfg.MinConsecutive = 3
	}
	if cfg.TopN <= 0 {
		cfg.TopN = 3
	}
	return &Stabilizer{
		counts:      make(map[string]int),
		cfg:         cfg,
		marketState: core.MarketOscillate,
	}
}

// SetMarketState injects the current market regime for entry confirmation
// adjustment. In OSCILLATE, the effective MinConsecutive is raised by 1
// to require stricter consecutive-tick confirmation before buying.
func (s *Stabilizer) SetMarketState(state core.MarketState) {
	s.mu.Lock()
	s.marketState = state
	s.mu.Unlock()
}

// Stabilize updates internal consecutive counts from the current tick's ranked
// signals and returns:
//
//   - stableSignals: subset of signals whose symbol has reached MinConsecutive
//   - counts: snapshot of all tracked symbols' current consecutive counts
//
// Symbols that drop out of the top-N have their count reset to 0.
func (s *Stabilizer) Stabilize(signals []core.Signal) ([]core.Signal, map[string]int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// ── Determine which symbols are currently in the top-N ───────────────────
	inTopN := make(map[string]bool, s.cfg.TopN)
	limit := s.cfg.TopN
	if limit > len(signals) {
		limit = len(signals)
	}
	for i := 0; i < limit; i++ {
		inTopN[signals[i].Symbol] = true
	}

	// ── Update consecutive counts ─────────────────────────────────────────────
	// Seed map with any new symbols seen this tick.
	for _, sig := range signals {
		if _, exists := s.counts[sig.Symbol]; !exists {
			s.counts[sig.Symbol] = 0
		}
	}
	// Increment symbols in TopN; reset those that fell out.
	for sym := range s.counts {
		if inTopN[sym] {
			s.counts[sym]++
		} else {
			s.counts[sym] = 0
		}
	}

	// ── Promote stable symbols ────────────────────────────────────────────────
	effectiveMin := s.cfg.MinConsecutive
	if s.marketState == core.MarketOscillate {
		effectiveMin = s.cfg.MinConsecutive + 1
	}
	stable := make([]core.Signal, 0, len(signals))
	for _, sig := range signals {
		if s.counts[sig.Symbol] >= effectiveMin {
			stable = append(stable, sig)
		}
	}

	// Return a counts snapshot (sorted by symbol for deterministic logging).
	countsCopy := make(map[string]int, len(s.counts))
	for k, v := range s.counts {
		countsCopy[k] = v
	}

	return stable, countsCopy
}

// SortedCounts returns a slice of (symbol, count) pairs sorted by symbol name,
// useful for deterministic log output.
func SortedCounts(counts map[string]int) []struct {
	Symbol string
	Count  int
} {
	type entry struct {
		Symbol string
		Count  int
	}
	out := make([]entry, 0, len(counts))
	for k, v := range counts {
		out = append(out, entry{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Symbol < out[j].Symbol })
	result := make([]struct {
		Symbol string
		Count  int
	}, len(out))
	for i, e := range out {
		result[i] = struct {
			Symbol string
			Count  int
		}{e.Symbol, e.Count}
	}
	return result
}
