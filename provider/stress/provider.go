// Package stress provides a DataProvider for pressure-testing the trading engine
// with structured 1000-tick scenarios that include:
//
//   - Bull / oscillate / bear market phases
//   - Two black-swan events (sudden crash −12%, sudden rally +10%)
//   - High-volatility periods (2× normal vol)
//   - Stock-to-index correlation (0.50–0.75 depending on sector)
//
// Architecture:
//
//	Provider.GetRealtime() is called once per engine tick.
//	The index ("000300") follows the pre-defined phase schedule.
//	Each stock return is a linear combination of the index return (correlation)
//	and an idiosyncratic component (independent noise).
//
//	stock_return = corr × index_return + √(1−corr²) × N(0, stock_vol) + stock_drift
//
// Phase schedule (total = 1000 ticks):
//
//	Phase        Ticks  Drift/tick  Vol/tick  Shock
//	BULL          150    +0.30%     2.0%      –
//	HIGH_VOL      100     0.00%     4.0%      –
//	CRASH          10    −1.50%     6.0%    −12% (tick 1)
//	BEAR           90    −0.20%     2.5%      –
//	RECOVERY      150    +0.20%     1.5%      –
//	OSCILLATE     100     0.00%     2.0%      –
//	RALLY          10    +1.50%     4.5%    +10% (tick 1)
//	STABILIZE      90    +0.10%     1.8%      –
//	BEAR2         150    −0.30%     2.5%      –
//	BULL2         150    +0.30%     1.8%      –
package stress

import (
	"math"
	"math/rand"
	"sync"
	"time"

	"astock_trade/core"
)

const (
	histLen  = 21
	emaAlpha = 2.0 / 21.0

	// IndexSymbol is the market-index placeholder symbol.
	IndexSymbol = "000300"

	// indexSeedPrice is the starting price for the synthetic index.
	indexSeedPrice = 4000.0
)

// ─── per-symbol rolling state ─────────────────────────────────────────────────

type symState struct {
	history []float64
	ema20   float64
}

func (s *symState) push(price float64) {
	s.history = append(s.history, price)
	if len(s.history) > histLen {
		s.history = s.history[1:]
	}
	s.ema20 = emaAlpha*price + (1-emaAlpha)*s.ema20
}

func (s *symState) last() float64 {
	if len(s.history) == 0 {
		return 0
	}
	return s.history[len(s.history)-1]
}

func (s *symState) returnND(n int) float64 {
	l := len(s.history)
	if l <= n || s.history[l-1-n] <= 0 {
		return 0
	}
	return (s.history[l-1] - s.history[l-1-n]) / s.history[l-1-n] * 100
}

func (s *symState) volatility() float64 {
	n := len(s.history)
	if n < 2 {
		return 0
	}
	rets := make([]float64, n-1)
	for i := range rets {
		if s.history[i] > 0 {
			rets[i] = (s.history[i+1] - s.history[i]) / s.history[i] * 100
		}
	}
	mean := 0.0
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	variance := 0.0
	for _, r := range rets {
		d := r - mean
		variance += d * d
	}
	variance /= float64(len(rets))
	v := math.Sqrt(variance)
	if math.IsNaN(v) {
		return 0
	}
	return v
}

// prewarm generates histLen synthetic ticks to initialise EMA and returns.
func prewarm(s *symState, seed float64, drift, vol float64, rng *rand.Rand) {
	s.ema20 = seed
	s.push(seed)
	for i := 1; i < histLen; i++ {
		prev := s.last()
		ret := rng.NormFloat64()*vol + drift
		s.push(math.Max(0.01, prev*(1+ret)))
	}
}

// ─── Phase ────────────────────────────────────────────────────────────────────

// Phase describes one market-environment segment.
type Phase struct {
	Ticks    int
	DriftPct float64 // per-tick expected return (e.g. 0.003 = +0.3%)
	VolPct   float64 // per-tick Gaussian std-dev (e.g. 0.020 = 2%)
	IsShock  bool    // if true, tick 1 of this phase applies an instant ShockPct
	ShockPct float64 // instantaneous price jump (e.g. −0.12 = −12% crash)
	Label    string
}

// DefaultPhases returns the 1000-tick pressure scenario.
func DefaultPhases() []Phase {
	return []Phase{
		{150, +0.003, 0.020, false, 0, "BULL"},
		{100, +0.000, 0.040, false, 0, "HIGH_VOL"},
		{10, -0.015, 0.060, true, -0.12, "CRASH"},    // black swan: −12% instant drop
		{90, -0.002, 0.025, false, 0, "BEAR"},
		{150, +0.002, 0.015, false, 0, "RECOVERY"},
		{100, +0.000, 0.020, false, 0, "OSCILLATE"},
		{10, +0.015, 0.045, true, +0.10, "RALLY"},    // black swan: +10% instant surge
		{90, +0.001, 0.018, false, 0, "STABILIZE"},
		{150, -0.003, 0.025, false, 0, "BEAR2"},
		{150, +0.003, 0.018, false, 0, "BULL2"},
		// Total = 150+100+10+90+150+100+10+90+150+150 = 1000 ticks
	}
}

// ─── SymbolParams ─────────────────────────────────────────────────────────────

// SymbolParams describes the idiosyncratic behaviour of one stock.
type SymbolParams struct {
	Drift float64 // per-tick drift bias (additional to phase drift)
	Vol   float64 // per-tick idiosyncratic volatility (std-dev)
	Corr  float64 // correlation with the index (0–1)
	Seed  float64 // initial stock price (CNY)
}

// DefaultSymbolParams returns varied parameters for the 20-symbol A-share universe.
func DefaultSymbolParams() map[string]SymbolParams {
	return map[string]SymbolParams{
		// CONSUMER – lower vol, moderate drift
		"600519": {+0.002, 0.018, 0.55, 1800.0}, // 茅台  (defensive premium)
		"000858": {+0.001, 0.022, 0.65, 200.0},  // 五粮液
		"600887": {+0.001, 0.020, 0.62, 40.0},   // 伊利
		"601888": {+0.001, 0.025, 0.65, 180.0},  // 中免
		// TECH – higher vol, higher corr
		"300750": {+0.002, 0.035, 0.72, 280.0}, // 宁德时代
		"002415": {+0.001, 0.028, 0.72, 40.0},  // 海康威视
		"000063": {+0.001, 0.032, 0.70, 25.0},  // 中兴通讯
		"600588": {+0.001, 0.026, 0.65, 35.0},  // 用友网络
		// FINANCE – low vol, high corr
		"600036": {+0.000, 0.016, 0.70, 40.0}, // 招商银行
		"601318": {+0.000, 0.020, 0.68, 60.0}, // 中国平安
		"601398": {+0.000, 0.015, 0.72, 5.0},  // 工商银行
		"601166": {+0.000, 0.018, 0.70, 20.0}, // 兴业银行
		// ENERGY – low corr (defensive)
		"600900": {+0.001, 0.014, 0.50, 20.0}, // 长江电力 (utility)
		"601985": {+0.001, 0.016, 0.55, 10.0}, // 中国核电
		"600028": {+0.000, 0.022, 0.75, 6.0},  // 中国石化
		"601088": {+0.001, 0.020, 0.72, 20.0}, // 中国神华
		// HEALTHCARE – lower corr
		"600276": {+0.001, 0.025, 0.55, 50.0},  // 恒瑞医药
		"000538": {+0.001, 0.022, 0.60, 100.0}, // 云南白药
		// INDUSTRIAL
		"000651": {+0.001, 0.025, 0.74, 40.0}, // 格力电器
		"601238": {+0.001, 0.028, 0.76, 20.0}, // 广汽集团
	}
}

// ─── Provider ─────────────────────────────────────────────────────────────────

// Provider satisfies core.DataProvider for stress testing.
type Provider struct {
	mu sync.Mutex

	rng       *rand.Rand
	phases    []Phase
	symParams map[string]SymbolParams
	cancelFn  func() // called after all phases complete

	// Scenario state
	tick        int
	phaseIdx    int
	tickInPhase int

	// Price states
	indexState  *symState
	stockStates map[string]*symState
}

// New creates a Provider.
// cancelFn is called once all phases have been processed (i.e., after 1000 ticks);
// pass a context.CancelFunc to stop the engine automatically.
func New(cancelFn func()) *Provider {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	phases := DefaultPhases()
	symParams := DefaultSymbolParams()

	// Pre-warm index state.
	idxState := &symState{ema20: indexSeedPrice}
	prewarm(idxState, indexSeedPrice, phases[0].DriftPct, phases[0].VolPct, rng)

	// Pre-warm each stock state.
	stockStates := make(map[string]*symState, len(symParams))
	for sym, p := range symParams {
		st := &symState{ema20: p.Seed}
		prewarm(st, p.Seed, phases[0].DriftPct+p.Drift, p.Vol, rng)
		stockStates[sym] = st
	}

	return &Provider{
		rng:         rng,
		phases:      phases,
		symParams:   symParams,
		cancelFn:    cancelFn,
		indexState:  idxState,
		stockStates: stockStates,
	}
}

// GetRealtime advances the scenario by one tick and returns quotes for all
// requested symbols (including the index).
func (p *Provider) GetRealtime(symbols []string) map[string]*core.Quote {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.tick++
	p.tickInPhase++

	// Determine current phase, clamped to valid range.
	if p.phaseIdx >= len(p.phases) {
		p.phaseIdx = len(p.phases) - 1
	}
	ph := p.phases[p.phaseIdx]

	// Phase transition: advance when current phase exhausted.
	if p.tickInPhase > ph.Ticks {
		if p.phaseIdx < len(p.phases)-1 {
			p.phaseIdx++
			p.tickInPhase = 1
			ph = p.phases[p.phaseIdx]
		} else {
			// All phases complete – signal the engine to stop.
			if p.cancelFn != nil {
				p.cancelFn()
				p.cancelFn = nil // prevent repeated cancel calls
			}
			// Continue generating with last phase parameters.
		}
	}

	// ── Advance index price ───────────────────────────────────────────────
	indexOldPrice := p.indexState.last()
	var indexReturn float64

	if ph.IsShock && p.tickInPhase == 1 {
		// Black-swan: instantaneous shock on first tick of the phase.
		indexReturn = ph.ShockPct
	} else {
		indexReturn = p.rng.NormFloat64()*ph.VolPct + ph.DriftPct
	}
	indexNewPrice := math.Max(0.01, indexOldPrice*(1+indexReturn))
	p.indexState.push(indexNewPrice)

	// ── Build result map ──────────────────────────────────────────────────
	result := make(map[string]*core.Quote, len(symbols))
	now := time.Now().UnixMilli()

	for _, sym := range symbols {
		if sym == IndexSymbol {
			spread := indexNewPrice * 0.001
			result[sym] = &core.Quote{
				Symbol:    sym,
				Price:     indexNewPrice,
				PrevClose: indexOldPrice,
				Bid1:      indexNewPrice - spread,
				Ask1:      indexNewPrice + spread,
				Volume:    50_000_000,
				PctChg:    indexReturn * 100,
				EMA20:     p.indexState.ema20,
				Timestamp: now,
			}
			continue
		}

		// ── Advance stock price (correlated with index) ───────────────────
		sp, known := p.symParams[sym]
		if !known {
			// Unknown symbol – generate a simple random walk.
			sp = SymbolParams{Drift: ph.DriftPct, Vol: ph.VolPct, Corr: 0.6, Seed: 50.0}
		}
		st, exists := p.stockStates[sym]
		if !exists {
			st = &symState{ema20: sp.Seed}
			prewarm(st, sp.Seed, ph.DriftPct+sp.Drift, sp.Vol, p.rng)
			p.stockStates[sym] = st
		}

		stockOldPrice := st.last()
		var stockReturn float64

		if ph.IsShock && p.tickInPhase == 1 {
			// Transmit shock proportional to correlation.
			stockReturn = sp.Corr*ph.ShockPct +
				math.Sqrt(1-sp.Corr*sp.Corr)*p.rng.NormFloat64()*sp.Vol*2
		} else {
			// Normal tick: correlated + idiosyncratic returns.
			idio := p.rng.NormFloat64() * sp.Vol
			stockReturn = sp.Corr*indexReturn +
				math.Sqrt(1-sp.Corr*sp.Corr)*idio + sp.Drift
		}

		stockNewPrice := math.Max(0.01, stockOldPrice*(1+stockReturn))
		st.push(stockNewPrice)

		spread := stockNewPrice * 0.001
		volume := int64(p.rng.Intn(5_000_000) + 500_000) // 50万~550万 simulated volume

		result[sym] = &core.Quote{
			Symbol:     sym,
			Price:      stockNewPrice,
			PrevClose:  stockOldPrice,
			Bid1:       stockNewPrice - spread,
			Ask1:       stockNewPrice + spread,
			Volume:     volume,
			PctChg:     stockReturn * 100,
			Return5d:   st.returnND(5),
			Return20d:  st.returnND(20),
			EMA20:      st.ema20,
			Volatility: st.volatility(),
			Timestamp:  now,
		}
	}
	return result
}

// CurrentPhaseLabel returns the label of the currently active scenario phase.
func (p *Provider) CurrentPhaseLabel() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.phaseIdx >= len(p.phases) {
		return "DONE"
	}
	return p.phases[p.phaseIdx].Label
}

// Tick returns the current tick count.
func (p *Provider) Tick() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tick
}

// Symbols returns all symbols in the stress universe.
func (p *Provider) Symbols() []string {
	syms := make([]string, 0, len(p.symParams))
	for sym := range p.symParams {
		syms = append(syms, sym)
	}
	return syms
}
