// Package risk implements the Portfolio Risk Engine – the central component
// responsible for keeping the system alive through extreme market events.
//
// # Architecture
//
// The engine attaches to two injection points without modifying engine.go:
//
//  1. ManagedPerf (wraps core.PerformanceTracker)
//     RecordEquity → Engine.Update(equity, tick) → portMgr.SetMaxTotalPct(effective)
//
//  2. ManagedPosMgr (wraps core.PositionManager)
//     CheckExit → if Engine says "liquidate" → return "STOP_LOSS" for every position
//
// # Feature 1 – Dynamic Position Scaling
//
//	DrawdownPct > 5%  → MaxTotalPct × 0.70
//	DrawdownPct > 10% → MaxTotalPct × 0.50
//	DrawdownPct > 15% → MaxTotalPct × 0.30
//
// # Feature 2 – Portfolio Hard Stop
//
//	DrawdownPct > 20% → ShouldLiquidate = true (exactly one tick)
//	                  → freeze trading for FreezeTicks ticks (MaxTotalPct = 0)
//
// # Feature 3 – Volatility-Driven Position Sizing
//
//	Rolling equity vol > VolHighThreshold → scale × VolHighScale (0.70)
//	Rolling equity vol < VolLowThreshold  → scale × VolLowScale  (1.10)
//
// # Feature 4 – Recovery Mechanism
//
//	While drawdown is recovering (dd < currentTierThreshold):
//	  currentDDScale ramps up by RecoveryRatePerTick each tick
//	  (smooth restoration prevents re-whipsawing)
package risk

import (
	"fmt"
	"log"
	"math"
	"sync"

	"astock_trade/core"
)

// ─── Config ───────────────────────────────────────────────────────────────────

// Config holds all tunable parameters for the Portfolio Risk Engine.
type Config struct {
	// Drawdown thresholds that activate each risk tier (fraction, 0–1).
	DD1        float64 // Tier1/CAUTION  threshold; default 0.05 (5%)
	DD2        float64 // Tier2/REDUCED  threshold; default 0.10 (10%)
	DD3        float64 // Tier3/DEFENSE  threshold; default 0.15 (15%)
	DDHardStop float64 // Hard-stop + freeze threshold; default 0.20 (20%)

	// Position scaling multipliers applied to BaseMaxTotalPct per tier.
	Scale1 float64 // CAUTION scale; default 0.70
	Scale2 float64 // REDUCED scale; default 0.50
	Scale3 float64 // DEFENSE scale; default 0.30

	// FreezeTicks: how many ticks to freeze new buys after a hard stop.
	FreezeTicks int // default 50

	// Volatility adjustment (computed from rolling equity returns).
	VolHighThreshold float64 // equity-vol % that triggers scale-down; default 2.5
	VolHighScale     float64 // multiplier when high vol; default 0.70
	VolLowThreshold  float64 // equity-vol % below which scale-up applies; default 0.8
	VolLowScale      float64 // multiplier when low vol (capped at 1.0 effective); default 1.15
	VolHistLen       int     // rolling window for equity-vol calculation; default 20

	// Recovery: ramp current scale up by this fraction per tick while recovering.
	RecoveryRatePerTick float64 // default 0.02 (2% of base per tick)

	// BaseMaxTotalPct: the maximum deployment fraction under no-risk conditions.
	BaseMaxTotalPct float64 // default 0.80
}

// Default returns a Config with production-grade defaults.
func Default() Config {
	return Config{
		DD1:        0.05,
		DD2:        0.10,
		DD3:        0.15,
		DDHardStop: 0.20,

		Scale1: 0.70,
		Scale2: 0.50,
		Scale3: 0.30,

		FreezeTicks: 50,

		VolHighThreshold: 2.5,
		VolHighScale:     0.70,
		VolLowThreshold:  0.8,
		VolLowScale:      1.15,
		VolHistLen:       20,

		RecoveryRatePerTick: 0.02,
		BaseMaxTotalPct:     0.80,
	}
}

// ─── Tier ─────────────────────────────────────────────────────────────────────

// Tier represents the current portfolio risk tier.
type Tier int

const (
	TierNormal  Tier = iota // drawdown ≤ DD1:              MaxTotalPct × 1.00
	TierCaution             // drawdown DD1–DD2:           MaxTotalPct × Scale1
	TierReduced             // drawdown DD2–DD3:           MaxTotalPct × Scale2
	TierDefense             // drawdown DD3–DDHardStop:    MaxTotalPct × Scale3
	TierFrozen              // drawdown > DDHardStop:      MaxTotalPct × 0.00 (+ liquidate)
)

func (t Tier) String() string {
	return [...]string{"NORMAL", "CAUTION", "REDUCED", "DEFENSE", "FROZEN"}[t]
}

// ─── RiskState ────────────────────────────────────────────────────────────────

// RiskState is a per-tick snapshot of the risk engine output.
type RiskState struct {
	Tick            int
	Tier            Tier
	DrawdownPct     float64 // current drawdown from peak equity (%)
	VolatilityPct   float64 // rolling equity volatility (%)
	DDScale         float64 // drawdown-driven scale factor (possibly smoothed by recovery)
	VolScale        float64 // volatility-driven scale factor
	EffectivePct    float64 // BaseMaxTotalPct × DDScale × VolScale (capped to base)
	IsFrozen        bool    // true during freeze period after hard stop
	FreezeTicksLeft int     // remaining freeze ticks
	ShouldLiquidate bool    // true for exactly ONE tick when hard stop fires
}

func (s RiskState) String() string {
	liq := ""
	if s.ShouldLiquidate {
		liq = " 🚨LIQUIDATE"
	}
	frz := ""
	if s.IsFrozen {
		frz = fmt.Sprintf(" FROZEN(%d)", s.FreezeTicksLeft)
	}
	return fmt.Sprintf("[RiskEngine] tick=%-4d  tier=%-8s  dd=%.2f%%  vol=%.2f%%  "+
		"ddScale=%.2f  volScale=%.2f  effectivePct=%.2f%%%s%s",
		s.Tick, s.Tier, s.DrawdownPct, s.VolatilityPct,
		s.DDScale, s.VolScale, s.EffectivePct*100, liq, frz)
}

// ─── Engine ───────────────────────────────────────────────────────────────────

// Engine is the core Portfolio Risk Engine.  Thread-safe.
type Engine struct {
	mu  sync.Mutex
	cfg Config

	// Equity tracking
	peakEquity    float64
	equityHistory []float64

	// Hard-stop state
	alreadyLiquidated bool // true after first hard-stop fires; reset on deep recovery
	frozenTicks       int  // remaining ticks in freeze period

	// Smooth position-scale (updated by recovery logic)
	currentDDScale float64

	// Statistics (exported via Stats())
	tierTicks    [5]int
	freezeEvents []FreezeEvent
	volatileAdj  int // times vol adjustment was applied
	lastTier     Tier
	tick         int

	// Last computed state (for CurrentState() queries between Update calls)
	lastState RiskState
}

// FreezeEvent records one hard-stop freeze.
type FreezeEvent struct {
	Tick        int
	DrawdownPct float64
	Duration    int
}

// New creates an Engine.
// initialEquity is the starting equity (e.g. initial capital).
func New(cfg Config, initialEquity float64) *Engine {
	d := Default()
	if cfg.DD1 <= 0 {
		cfg.DD1 = d.DD1
	}
	if cfg.DD2 <= 0 {
		cfg.DD2 = d.DD2
	}
	if cfg.DD3 <= 0 {
		cfg.DD3 = d.DD3
	}
	if cfg.DDHardStop <= 0 {
		cfg.DDHardStop = d.DDHardStop
	}
	if cfg.Scale1 <= 0 {
		cfg.Scale1 = d.Scale1
	}
	if cfg.Scale2 <= 0 {
		cfg.Scale2 = d.Scale2
	}
	if cfg.Scale3 <= 0 {
		cfg.Scale3 = d.Scale3
	}
	if cfg.FreezeTicks <= 0 {
		cfg.FreezeTicks = d.FreezeTicks
	}
	if cfg.VolHighThreshold <= 0 {
		cfg.VolHighThreshold = d.VolHighThreshold
	}
	if cfg.VolHighScale <= 0 {
		cfg.VolHighScale = d.VolHighScale
	}
	if cfg.VolLowThreshold <= 0 {
		cfg.VolLowThreshold = d.VolLowThreshold
	}
	if cfg.VolLowScale <= 0 {
		cfg.VolLowScale = d.VolLowScale
	}
	if cfg.VolHistLen <= 0 {
		cfg.VolHistLen = d.VolHistLen
	}
	if cfg.RecoveryRatePerTick <= 0 {
		cfg.RecoveryRatePerTick = d.RecoveryRatePerTick
	}
	if cfg.BaseMaxTotalPct <= 0 {
		cfg.BaseMaxTotalPct = d.BaseMaxTotalPct
	}
	return &Engine{
		cfg:            cfg,
		peakEquity:     initialEquity,
		currentDDScale: 1.0,
		lastTier:       TierNormal,
		lastState: RiskState{
			Tier:         TierNormal,
			DDScale:      1.0,
			VolScale:     1.0,
			EffectivePct: cfg.BaseMaxTotalPct,
		},
	}
}

// Update advances the engine by one tick.
//
// Call this in the same phase as RecordEquity (i.e., end of each tick after
// buys/sells have been processed).  Returns the new RiskState that should be
// applied to portMgr.SetMaxTotalPct.
func (e *Engine) Update(equity float64) RiskState {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.tick++

	// ── Update equity history ─────────────────────────────────────────────
	e.equityHistory = append(e.equityHistory, equity)
	if len(e.equityHistory) > e.cfg.VolHistLen {
		e.equityHistory = e.equityHistory[1:]
	}

	// ── Update peak ───────────────────────────────────────────────────────
	if equity > e.peakEquity {
		e.peakEquity = equity
	}

	// ── Drawdown ──────────────────────────────────────────────────────────
	dd := 0.0
	if e.peakEquity > 0 {
		dd = (e.peakEquity - equity) / e.peakEquity
	}

	// ── Equity volatility (combined fast+slow window) ─────────────────────
	// Short window (5 ticks) detects HIGH_VOL onset quickly;
	// long window (VolHistLen) smooths noise.  Combined = average of the two.
	// Expected levels: BULL≈0.67%  HIGH_VOL≈1.34%  → threshold 1.0% sits cleanly
	// in between, but averaging makes the signal cross the threshold more reliably.
	shortVol := e.equityVolatility(5)
	longVol := e.equityVolatility(e.cfg.VolHistLen)
	vol := (shortVol + longVol) / 2

	// ── Advance freeze countdown ──────────────────────────────────────────
	if e.frozenTicks > 0 {
		e.frozenTicks--
	}

	// ── Hard stop (Feature 2) ─────────────────────────────────────────────
	needsLiquidation := false
	if dd >= e.cfg.DDHardStop && !e.alreadyLiquidated {
		e.alreadyLiquidated = true
		e.frozenTicks = e.cfg.FreezeTicks
		needsLiquidation = true
		e.freezeEvents = append(e.freezeEvents, FreezeEvent{
			Tick:        e.tick,
			DrawdownPct: dd * 100,
			Duration:    e.cfg.FreezeTicks,
		})
		log.Printf("  🚨 [RiskEngine] HARD STOP! tick=%d  drawdown=%.2f%%  freeze=%d ticks",
			e.tick, dd*100, e.cfg.FreezeTicks)
	}
	// Reset alreadyLiquidated after significant recovery (allows re-triggering
	// if there is a second major crash).
	if dd < e.cfg.DDHardStop*0.4 {
		e.alreadyLiquidated = false
	}

	// ── Drawdown tier & target scale (Feature 1) ──────────────────────────
	var tier Tier
	var targetDDScale float64
	switch {
	case e.frozenTicks > 0:
		tier = TierFrozen
		targetDDScale = 0.0
	case dd >= e.cfg.DD3:
		tier = TierDefense
		targetDDScale = e.cfg.Scale3
	case dd >= e.cfg.DD2:
		tier = TierReduced
		targetDDScale = e.cfg.Scale2
	case dd >= e.cfg.DD1:
		tier = TierCaution
		targetDDScale = e.cfg.Scale1
	default:
		tier = TierNormal
		targetDDScale = 1.0
	}

	// Log tier transitions.
	if tier != e.lastTier {
		log.Printf("  📊 [RiskEngine] tier %s → %s  dd=%.2f%%  tick=%d",
			e.lastTier, tier, dd*100, e.tick)
		e.lastTier = tier
	}

	// ── Smooth scale (Feature 4 – Recovery) ──────────────────────────────
	// Downside: snap immediately; upside (recovery): ramp gradually.
	if e.currentDDScale > targetDDScale {
		e.currentDDScale = targetDDScale // immediate reduction
	} else if e.currentDDScale < targetDDScale {
		// Gradual recovery
		e.currentDDScale = math.Min(
			targetDDScale,
			e.currentDDScale+e.cfg.RecoveryRatePerTick,
		)
		if e.currentDDScale >= targetDDScale-0.001 {
			log.Printf("  🔄 [RiskEngine] Recovery complete: ddScale=%.2f  tier=%s",
				e.currentDDScale, tier)
		}
	}

	// ── Volatility scale (Feature 3) ──────────────────────────────────────
	volScale := 1.0
	if vol > e.cfg.VolHighThreshold {
		volScale = e.cfg.VolHighScale
		e.volatileAdj++
	} else if vol < e.cfg.VolLowThreshold && tier == TierNormal {
		// Only scale up when in normal regime to avoid partially-offsetting reductions.
		volScale = e.cfg.VolLowScale
	}

	// ── Effective MaxTotalPct ─────────────────────────────────────────────
	effectivePct := e.cfg.BaseMaxTotalPct * e.currentDDScale * volScale
	effectivePct = math.Max(0, math.Min(e.cfg.BaseMaxTotalPct, effectivePct))

	// ── Tier statistics ───────────────────────────────────────────────────
	e.tierTicks[tier]++

	state := RiskState{
		Tick:            e.tick,
		Tier:            tier,
		DrawdownPct:     dd * 100,
		VolatilityPct:   vol,
		DDScale:         e.currentDDScale,
		VolScale:        volScale,
		EffectivePct:    effectivePct,
		IsFrozen:        e.frozenTicks > 0,
		FreezeTicksLeft: e.frozenTicks,
		ShouldLiquidate: needsLiquidation,
	}
	e.lastState = state
	return state
}

// CurrentState returns the last RiskState without running a new calculation.
// Safe to call from any goroutine between Update() calls.
func (e *Engine) CurrentState() RiskState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastState
}

// equityVolatility computes the rolling std-dev of equity % returns using the
// last `window` equity samples (or fewer if history is shorter).
// Must be called with e.mu held.
func (e *Engine) equityVolatility(window int) float64 {
	n := len(e.equityHistory)
	if n < 2 {
		return 0
	}
	// Use at most `window` most-recent samples.
	if n > window+1 {
		n = window + 1
	}
	hist := e.equityHistory[len(e.equityHistory)-n:]
	rets := make([]float64, n-1)
	for i := range rets {
		if hist[i] > 0 {
			rets[i] = (hist[i+1] - hist[i]) / hist[i] * 100
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
	v := math.Sqrt(variance / float64(len(rets)))
	if math.IsNaN(v) {
		return 0
	}
	return v
}

// Stats returns a human-readable summary of the engine's activity.
func (e *Engine) Stats() EngineStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EngineStats{
		TierTicks:    e.tierTicks,
		TotalTicks:   e.tick,
		FreezeEvents: append([]FreezeEvent{}, e.freezeEvents...),
		VolAdjCount:  e.volatileAdj,
		FinalState:   e.lastState,
	}
}

// EngineStats summarises the risk engine's activity over the backtest.
type EngineStats struct {
	TierTicks    [5]int       // ticks spent in each tier
	TotalTicks   int
	FreezeEvents []FreezeEvent
	VolAdjCount  int // number of ticks with high-vol position reduction
	FinalState   RiskState
}

// ─── ManagedPerf ─────────────────────────────────────────────────────────────

// ManagedPerf wraps core.PerformanceTracker and runs the risk engine on every
// RecordEquity call.  It is also responsible for updating portMgr.SetMaxTotalPct
// in real-time, making it the single authoritative source for MaxTotalPct.
type ManagedPerf struct {
	core.PerformanceTracker

	riskEngine *Engine
	portMgr    core.MaxTotalPctSetter

	mu        sync.Mutex
	lastState RiskState
}

// NewManagedPerf creates a ManagedPerf.
// portMgr must implement core.MaxTotalPctSetter (portfolio.Manager does).
func NewManagedPerf(
	inner core.PerformanceTracker,
	engine *Engine,
	portMgr core.MaxTotalPctSetter,
) *ManagedPerf {
	return &ManagedPerf{
		PerformanceTracker: inner,
		riskEngine:         engine,
		portMgr:            portMgr,
		lastState:          engine.CurrentState(),
	}
}

// RecordEquity overrides the inner tracker to inject risk-engine logic.
// Order of operations:
//  1. Update risk engine → get new RiskState
//  2. Apply effective MaxTotalPct to portfolio manager
//  3. Delegate equity recording to inner tracker
func (mp *ManagedPerf) RecordEquity(equity float64) {
	state := mp.riskEngine.Update(equity)

	mp.mu.Lock()
	mp.lastState = state
	mp.mu.Unlock()

	mp.portMgr.SetMaxTotalPct(state.EffectivePct)
	mp.PerformanceTracker.RecordEquity(equity)
}

// RiskState returns the most recent risk state.
func (mp *ManagedPerf) RiskState() RiskState {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	return mp.lastState
}

// Compile-time check.
var _ core.PerformanceTracker = (*ManagedPerf)(nil)

// ─── ManagedPosMgr ────────────────────────────────────────────────────────────

// ManagedPosMgr wraps core.PositionManager and injects portfolio-level forced
// liquidation when the risk engine fires a hard stop.
//
// When Engine.CurrentState().ShouldLiquidate is true, CheckExit returns
// "STOP_LOSS" for every position, bypassing ExecController MinHoldTicks guards
// and causing the engine to close all holdings in Phase 1.
type ManagedPosMgr struct {
	core.PositionManager
	riskEngine *Engine
}

// NewManagedPosMgr creates a ManagedPosMgr.
func NewManagedPosMgr(inner core.PositionManager, engine *Engine) *ManagedPosMgr {
	return &ManagedPosMgr{PositionManager: inner, riskEngine: engine}
}

// CheckExit overrides the inner CheckExit to inject forced portfolio liquidation.
// The liquidation signal is active for exactly one tick (the tick immediately
// after the hard-stop threshold is crossed in Engine.Update).
//
// Additionally, in DEFENSE tier (DD > 15%), any position with unrealised loss
// is exited immediately ("止血" – stop the bleeding).  Profitable positions
// continue with their normal trail/take-profit logic, protecting gains.
func (m *ManagedPosMgr) CheckExit(pos *core.Position, q *core.Quote) string {
	state := m.riskEngine.CurrentState()

	// Feature 2: hard stop – close everything (overrides all other logic).
	if state.ShouldLiquidate {
		log.Printf("  🚨 [RiskEngine] FORCE EXIT %-8s (portfolio hard stop)", pos.Symbol)
		return "STOP_LOSS"
	}

	// Feature 1 / DEFENSE tier: close losing positions immediately to stop further
	// drawdown accumulation.  Winning positions are left to their normal exits.
	if state.Tier >= TierDefense && q != nil && pos.AvgPrice > 0 {
		pnl := (q.Price - pos.AvgPrice) / pos.AvgPrice
		if pnl < 0 {
			log.Printf("  🛡️ [RiskEngine] DEFENSE EXIT %-8s pnl=%.2f%% (tier=%s dd=%.1f%%)",
				pos.Symbol, pnl*100, state.Tier, state.DrawdownPct)
			return "STOP_LOSS"
		}
	}

	return m.PositionManager.CheckExit(pos, q)
}

// Compile-time check.
var _ core.PositionManager = (*ManagedPosMgr)(nil)
