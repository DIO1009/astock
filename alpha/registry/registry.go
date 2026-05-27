// Package registry provides an AlphaEngine whose per-strategy weights evolve
// dynamically based on attributed trade performance.
//
// Architecture:
//
//	Registry implements core.AlphaEngine (Rank) and core.StrategyRegistry
//	(RecordBuy, RecordSell, WeightSnapshot).
//
// Weight update algorithm (every UpdateEvery ticks):
//
//  1. For each strategy compute a performance score:
//     perf_i = 0.5×win_rate_i + 0.5×sigmoid(avg_pnl_i / SigmoidScale)
//     (0.5 = neutral when no trades; 1.0 = perfect; 0.0 = terrible)
//  2. Compute adaptation factor:
//     factor_i = 1 + Lambda × (2×perf_i − 1)
//     → perf=1.0 → factor = 1+Lambda  (boost)
//     → perf=0.5 → factor = 1.0       (unchanged)
//     → perf=0.0 → factor = 1−Lambda  (reduce)
//  3. new_weight_i = base_weight_i × factor_i
//     clamped to [base_weight_i×MinFactor, base_weight_i×MaxFactor]
//  4. Weights are used raw by Rank (which normalises by total weight).
//
// Attribution: when a BUY fires, the strategy with the highest contribution in
// Signal.Breakdown is recorded as "dominant".  When the resulting trade closes,
// its PnL is attributed entirely to that dominant strategy.
package registry

import (
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"

	"astock_trade/core"
)

type crossSectionalAlpha interface {
	CrossSectionalScores(quotes map[string]*core.Quote) map[string]float64
}

// Entry pairs an AlphaStrategy with its baseline importance weight.
type Entry struct {
	Alpha      core.AlphaStrategy
	BaseWeight float64 // relative baseline, e.g. 0.4
}

// Config holds tunable parameters for the registry.
type Config struct {
	// UpdateEvery: ticks between weight recalculations. Default 20.
	UpdateEvery int

	// Lambda: adaptation speed in [0, 1].
	// 0 = frozen weights (no adaptation).
	// 0.5 = dominant strategy gets 1.5× base weight, worst gets 0.5×.
	// Default 0.40.
	Lambda float64

	// MinFactor / MaxFactor: clamp weight multiplier to prevent extreme swings.
	// Default 0.20 / 3.0.
	MinFactor float64
	MaxFactor float64
}

// stratPerf tracks per-strategy attributed closed-trade statistics.
type stratPerf struct {
	wins     int
	losses   int
	totalPnL float64
}

// Registry satisfies both core.AlphaEngine and core.StrategyRegistry.
type Registry struct {
	mu          sync.Mutex
	entries     []entry // strategies + live weights
	perfs       map[string]*stratPerf
	openAttrib  map[string]string // symbol → dominant strategy name at BUY time
	cfg         Config
	currentTick int
	lastUpdate  int
	marketState core.MarketState
}

// entry is the internal mutable form of Entry.
type entry struct {
	alpha      core.AlphaStrategy
	name       string
	baseWeight float64
	weight     float64 // current dynamic weight
}

// New creates a Registry.  Panics if no entries are provided.
func New(cfg Config, entries ...Entry) *Registry {
	if len(entries) == 0 {
		panic("registry.New: at least one Entry required")
	}
	if cfg.UpdateEvery <= 0 {
		cfg.UpdateEvery = 20
	}
	if cfg.Lambda < 0 {
		cfg.Lambda = 0
	}
	if cfg.Lambda > 1 {
		cfg.Lambda = 1
	}
	if cfg.MinFactor <= 0 {
		cfg.MinFactor = 0.20
	}
	if cfg.MaxFactor <= 0 {
		cfg.MaxFactor = 3.0
	}

	r := &Registry{
		cfg:        cfg,
		entries:    make([]entry, len(entries)),
		perfs:      make(map[string]*stratPerf, len(entries)),
		openAttrib: make(map[string]string),
	}
	for i, e := range entries {
		name := e.Alpha.Name()
		r.entries[i] = entry{
			alpha:      e.Alpha,
			name:       name,
			baseWeight: e.BaseWeight,
			weight:     e.BaseWeight,
		}
		r.perfs[name] = &stratPerf{}
	}
	r.marketState = core.MarketOscillate
	return r
}

// SetMarketState injects the current market regime for scoring-time weight gating.
func (r *Registry) SetMarketState(state core.MarketState) {
	r.mu.Lock()
	r.marketState = state
	r.mu.Unlock()
}

// ── core.AlphaEngine ─────────────────────────────────────────────────────────

// Rank scores every symbol in quotes using current dynamic weights and returns
// signals sorted descending by aggregate score.
// Also advances the internal tick counter and triggers weight updates.
func (r *Registry) Rank(quotes map[string]*core.Quote) []core.Signal {
	r.mu.Lock()
	r.currentTick++
	if r.cfg.UpdateEvery > 0 && r.currentTick-r.lastUpdate >= r.cfg.UpdateEvery {
		r.updateWeightsLocked()
		r.lastUpdate = r.currentTick
	}
	// Snapshot weights for this tick (avoid holding lock during Score calls).
	snap := make([]entry, len(r.entries))
	copy(snap, r.entries)
	state := r.marketState
	r.mu.Unlock()

	signals := make([]core.Signal, 0, len(quotes))
	crossScores := make([]map[string]float64, len(snap))
	for i, e := range snap {
		if alpha, ok := e.alpha.(crossSectionalAlpha); ok {
			crossScores[i] = alpha.CrossSectionalScores(quotes)
		}
	}
	for sym, q := range quotes {
		breakdown := make(map[string]float64, len(snap))
		weighted := 0.0
		totalWeight := 0.0
		for i, e := range snap {
			raw := 0.0
			if scores := crossScores[i]; scores != nil {
				raw = scores[sym]
			} else {
				raw = e.alpha.Score(q)
			}
			weight := gatedWeight(e.name, e.weight, state)
			clamped := math.Max(-1, math.Min(1, raw))
			breakdown[e.name] = clamped
			weighted += clamped * weight
			totalWeight += weight
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
	sort.Slice(signals, func(i, j int) bool {
		return signals[i].Score > signals[j].Score
	})
	return signals
}

func gatedWeight(name string, weight float64, state core.MarketState) float64 {
	switch state {
	case core.MarketUptrend:
		if name == "reversal" {
			return weight * 0.5
		}
	case core.MarketDowntrend:
		if name == "momentum" {
			return weight * 0.5
		}
	}
	return weight
}

// ── core.StrategyRegistry ────────────────────────────────────────────────────

// RecordBuy notes which strategy dominated the BUY signal for later attribution.
func (r *Registry) RecordBuy(symbol string, breakdown map[string]float64) {
	dominant := dominantStrategy(breakdown)
	if dominant == "" {
		return
	}
	r.mu.Lock()
	r.openAttrib[symbol] = dominant
	r.mu.Unlock()
}

// RecordSell attributes the trade outcome to the dominant strategy at entry.
func (r *Registry) RecordSell(symbol string, pnlPct float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dominant, ok := r.openAttrib[symbol]
	if !ok {
		return
	}
	delete(r.openAttrib, symbol)
	p, exists := r.perfs[dominant]
	if !exists {
		return
	}
	p.totalPnL += pnlPct
	if pnlPct > 0 {
		p.wins++
	} else {
		p.losses++
	}
}

// WeightSnapshot returns a snapshot of all strategies' current state.
func (r *Registry) WeightSnapshot() []core.StrategyWeight {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.StrategyWeight, len(r.entries))
	for i, e := range r.entries {
		p := r.perfs[e.name]
		trades := p.wins + p.losses
		sw := core.StrategyWeight{
			Name:       e.name,
			Weight:     e.weight,
			BaseWeight: e.baseWeight,
			TradeCount: trades,
		}
		if trades > 0 {
			sw.WinRate = float64(p.wins) / float64(trades) * 100
			sw.AvgPnL = p.totalPnL / float64(trades)
		}
		out[i] = sw
	}
	return out
}

// ── internal helpers ─────────────────────────────────────────────────────────

// updateWeightsLocked recalculates all strategy weights based on attribution
// performance. Must be called under r.mu.
func (r *Registry) updateWeightsLocked() {
	type update struct {
		idx  int
		oldW float64
		newW float64
		perf float64
	}
	updates := make([]update, len(r.entries))

	for i, e := range r.entries {
		p := r.perfs[e.name]
		trades := p.wins + p.losses

		perf := 0.5 // neutral default when no attribution data
		if trades > 0 {
			winRate := float64(p.wins) / float64(trades)
			avgPnL := p.totalPnL / float64(trades)
			// sigmoid(avgPnL/5): 0.5 at 0%, ~0.73 at +5%, ~0.27 at -5%
			sigPnL := 1.0 / (1.0 + math.Exp(-avgPnL/5.0))
			perf = 0.5*winRate + 0.5*sigPnL
		}

		factor := 1.0 + r.cfg.Lambda*(2*perf-1)
		factor = math.Max(r.cfg.MinFactor, math.Min(r.cfg.MaxFactor, factor))
		newW := e.baseWeight * factor

		updates[i] = update{idx: i, oldW: e.weight, newW: newW, perf: perf}
		r.entries[i].weight = newW
	}

	// Log weight changes.
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"── [策略权重更新 tick=%d] ───────────────────────────────────────\n",
		r.currentTick))
	for _, u := range updates {
		e := r.entries[u.idx]
		p := r.perfs[e.name]
		trades := p.wins + p.losses
		wr := 0.0
		avgPnL := 0.0
		if trades > 0 {
			wr = float64(p.wins) / float64(trades) * 100
			avgPnL = p.totalPnL / float64(trades)
		}
		change := ""
		if math.Abs(u.newW-u.oldW) > u.oldW*0.05 { // >5% change
			if u.newW > u.oldW {
				change = "↑"
			} else {
				change = "↓"
			}
		}
		b.WriteString(fmt.Sprintf(
			"   %-12s weight=%+.3f→%.3f%s  base=%.3f  perf=%.2f  WR=%5.1f%%  avgPnL=%+.2f%%  trades=%d\n",
			e.name, u.oldW, u.newW, change, e.baseWeight, u.perf, wr, avgPnL, trades))
	}
	log.Print(b.String())
}

// dominantStrategy returns the name of the strategy with the highest positive
// score contribution in the breakdown map.
// Returns "" if no strategy has a positive score.
func dominantStrategy(breakdown map[string]float64) string {
	best := ""
	bestScore := 0.0
	for name, score := range breakdown {
		// Skip the internal "_dampened" pseudo-entry added by the aggregator.
		if strings.HasPrefix(name, "_") {
			continue
		}
		if score > bestScore {
			bestScore = score
			best = name
		}
	}
	return best
}
