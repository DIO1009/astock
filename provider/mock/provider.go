// Package mock provides a DataProvider that simulates realistic price series
// using a correlated random walk.
//
// Design principles that fix the "always-zero EMA" and "independent-random" bugs:
//
//  1. Price walk: each tick applies a small normally-distributed drift to the
//     previous price, so successive prices are correlated.
//  2. Pre-warm: when a symbol is first seen, 20 warm-up ticks are generated so
//     EMA20, Return5d, Return20d, and Volatility are all non-zero from tick 1.
//  3. All derived fields (EMA20, Return5d, Return20d, Volatility) are computed
//     here and stamped into Quote; strategies must NOT maintain their own history.
package mock

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"astock_trade/core"
)

// histLen is the maximum price history window kept per symbol.
// 21 prices → 20 returns → enough for Return20d and Volatility(20).
const histLen = 21

// emaAlpha is the EMA-20 smoothing factor: 2/(20+1).
const emaAlpha = 2.0 / 21.0

// tickVolStdDev is the per-tick log-return std-dev (≈ 2.0 % intraday oscillation).
// A higher value creates more price oscillation, which exercises trailing-stop logic.
const tickVolStdDev = 0.020

// tickDrift is a small upward bias added to each tick's return so that winning
// positions can reach take-profit / trailing-stop targets within a short demo.
// 0.003 = +0.3% per tick expected drift (≈75% annual gain equivalent for demo).
const tickDrift = 0.003

// symbolState holds the per-symbol rolling price history and EMA.
// Access is NOT individually locked; the Provider holds a single global mutex.
type symbolState struct {
	history []float64 // fixed-length ring: oldest→newest, max histLen entries
	ema20   float64
}

// push appends price to history (evicting the oldest if at capacity) and
// updates the EMA.
func (s *symbolState) push(price float64) {
	s.history = append(s.history, price)
	if len(s.history) > histLen {
		s.history = s.history[1:]
	}
	s.ema20 = emaAlpha*price + (1-emaAlpha)*s.ema20
}

// last returns the most-recently pushed price, or 0 if history is empty.
func (s *symbolState) last() float64 {
	if len(s.history) == 0 {
		return 0
	}
	return s.history[len(s.history)-1]
}

// returnND returns the N-tick price return in percent.
// Returns 0 when there is insufficient history.
func (s *symbolState) returnND(n int) float64 {
	l := len(s.history)
	if l <= n {
		return 0
	}
	past := s.history[l-1-n]
	if past <= 0 {
		return 0
	}
	return (s.history[l-1] - past) / past * 100
}

// volatility returns the rolling std-dev of per-tick log-returns (as %).
// Uses all available history up to histLen ticks.
func (s *symbolState) volatility() float64 {
	n := len(s.history)
	if n < 2 {
		return 0
	}
	returns := make([]float64, n-1)
	for i := 0; i < n-1; i++ {
		if s.history[i] > 0 {
			returns[i] = (s.history[i+1] - s.history[i]) / s.history[i] * 100
		}
	}
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))
	variance := 0.0
	for _, r := range returns {
		d := r - mean
		variance += d * d
	}
	variance /= float64(len(returns))
	v := math.Sqrt(variance)
	if math.IsNaN(v) {
		return 0
	}
	return v
}

// ─────────────────────────────────────────────────────────────────────────────

// Provider satisfies core.DataProvider.
type Provider struct {
	mu     sync.Mutex
	rng    *rand.Rand
	states map[string]*symbolState
}

// New returns a Provider seeded from the current clock.
func New() *Provider {
	return &Provider{
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
		states: make(map[string]*symbolState),
	}
}

// nextPrice advances the random walk by one tick from prev.
// A small positive drift is applied so that positions can realistically
// reach take-profit and trailing-stop targets within a demo window.
func (p *Provider) nextPrice(prev float64) float64 {
	drift := p.rng.NormFloat64()*tickVolStdDev + tickDrift
	price := prev * (1 + drift)
	return math.Max(1.0, price) // floor at 1 CNY
}

// initSymbol seeds a new symbolState and pre-warms it with histLen ticks so
// that all derived fields are non-zero from the very first real tick.
func (p *Provider) initSymbol(seedPrice float64) *symbolState {
	s := &symbolState{
		history: make([]float64, 0, histLen),
		ema20:   seedPrice,
	}
	// First entry – seed the EMA
	s.push(seedPrice)
	// Pre-warm: simulate histLen−1 additional ticks
	for i := 1; i < histLen; i++ {
		s.push(p.nextPrice(s.last()))
	}
	return s
}

// GetRealtime returns a Quote for each symbol, computing all derived fields
// from the running price history.
//
// On the first call for a symbol, the state is initialised and pre-warmed so
// that EMA20, Return5d, Return20d, and Volatility are immediately meaningful.
func (p *Provider) GetRealtime(symbols []string) map[string]*core.Quote {
	p.mu.Lock()
	defer p.mu.Unlock()

	result := make(map[string]*core.Quote, len(symbols))
	now := time.Now().UnixMilli()

	for _, sym := range symbols {
		state, ok := p.states[sym]
		if !ok {
			seed := 10.0 + p.rng.Float64()*90.0
			state = p.initSymbol(seed)
			p.states[sym] = state
		}

		prevClose := state.last()
		price := p.nextPrice(prevClose)
		state.push(price)

		spread := price * 0.001
		pctChg := 0.0
		if prevClose > 0 {
			pctChg = (price - prevClose) / prevClose * 100
		}

		result[sym] = &core.Quote{
			Symbol:     sym,
			Price:      price,
			PrevClose:  prevClose,
			Bid1:       price - spread,
			Ask1:       price + spread,
			Volume:     p.randomVolume(),
			PctChg:     pctChg,
			Return5d:   state.returnND(5),
			Return20d:  state.returnND(20),
			EMA20:      state.ema20,
			Volatility: state.volatility(),
			Timestamp:  now,
		}
	}
	return result
}

// randomVolume returns a plausible simulated volume in [100k, 1.1M].
func (p *Provider) randomVolume() int64 {
	return int64(p.rng.Intn(1_000_000) + 100_000)
}
