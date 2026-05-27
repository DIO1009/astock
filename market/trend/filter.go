// Package trend provides a MarketFilter implementing three-state broad-market
// classification: UPTREND / OSCILLATE / DOWNTREND.
//
// Classification algorithm (per tick):
//  1. Maintain a rolling SMA over the last Period index prices.
//  2. Compute deviation: dev = (price − MA) / MA
//  3. Classify:
//       dev ≥  +UptrendThreshold   → UPTREND
//       dev ≤  −DowntrendThreshold → DOWNTREND
//       otherwise                  → OSCILLATE
//
//  AllowOpen:
//    - UPTREND / OSCILLATE → true
//    - DOWNTREND           → false
//
// Thread-safe. Both AllowOpen and State call update() internally; calling
// both in the same tick is safe — the window advances only once per unique
// price value due to the idempotent push design (last-seen cache).
package trend

import (
	"sync"

	"astock_trade/core"
)

// Config holds tunable parameters.
type Config struct {
	// Period is the SMA look-back window. Default 5.
	Period int

	// UptrendThreshold: min positive deviation from MA to classify as UPTREND.
	// Default 0.005 (0.5 %).
	UptrendThreshold float64

	// DowntrendThreshold: min negative deviation from MA to classify as DOWNTREND.
	// Default 0.005 (0.5 %).
	DowntrendThreshold float64
}

// Filter satisfies core.MarketFilter.
type Filter struct {
	mu        sync.Mutex
	history   []float64 // rolling window of index prices
	cfg       Config
	lastPrice float64 // price from the most recent push (dedup guard)
	lastState core.MarketState
	warmedUp  bool
}

// New returns a Filter with defaults applied for zero-value fields.
func New(cfg Config) *Filter {
	if cfg.Period <= 0 {
		cfg.Period = 5
	}
	if cfg.UptrendThreshold <= 0 {
		cfg.UptrendThreshold = 0.005
	}
	if cfg.DowntrendThreshold <= 0 {
		cfg.DowntrendThreshold = 0.005
	}
	return &Filter{
		history:   make([]float64, 0, cfg.Period+1),
		cfg:       cfg,
		lastState: core.MarketOscillate,
	}
}

// State classifies the current market regime.
// Each unique price advances the internal SMA window exactly once.
func (f *Filter) State(indexQuote *core.Quote) core.MarketState {
	if indexQuote == nil {
		return core.MarketOscillate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.update(indexQuote.Price)
	return f.lastState
}

// AllowOpen returns false only during confirmed DOWNTREND.
func (f *Filter) AllowOpen(indexQuote *core.Quote) bool {
	if indexQuote == nil {
		return true
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.update(indexQuote.Price)
	return f.lastState != core.MarketDowntrend
}

// update pushes a new price into the SMA window and recomputes lastState.
// Idempotent for the same price value within a tick (dedup by exact equality).
// Must be called under f.mu.
func (f *Filter) update(price float64) {
	// Dedup: if same price was already processed this tick, skip re-push.
	if price == f.lastPrice && f.warmedUp {
		return
	}
	f.lastPrice = price
	f.warmedUp = true

	f.history = append(f.history, price)
	if len(f.history) > f.cfg.Period {
		f.history = f.history[1:]
	}

	if len(f.history) < f.cfg.Period {
		f.lastState = core.MarketOscillate // warming up
		return
	}

	sum := 0.0
	for _, p := range f.history {
		sum += p
	}
	ma := sum / float64(len(f.history))
	if ma <= 0 {
		f.lastState = core.MarketOscillate
		return
	}

	dev := (price - ma) / ma
	switch {
	case dev >= f.cfg.UptrendThreshold:
		f.lastState = core.MarketUptrend
	case dev <= -f.cfg.DowntrendThreshold:
		f.lastState = core.MarketDowntrend
	default:
		f.lastState = core.MarketOscillate
	}
}
