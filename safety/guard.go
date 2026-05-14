// Package safety implements the final safety control layer for live trading.
//
// SafetyGuard sits above all other risk layers (Portfolio Risk Engine,
// AdaptiveOptimizer) and provides three independent protection mechanisms:
//
// # Feature 1 – Losing-Streak Suppression
//
//	CurrentStreak ≥ StreakHalfPositionAt (default 10)
//	  → apply StreakScale=StreakPositionScale (default 0.5) to portMgr.SetMaxTotalPct
//	    (half position sizing by default)
//
//	CurrentStreak ≥ StreakFreezeAt (default 15)
//	  → freeze new openings for StreakFreezeTicks (default 12) ticks
//	    (effectively a per-streak kill switch)
//
// # Feature 2 – Operator Manual Controls
//
//	StopOpening()        – immediately blocks any new BUY
//	ResumeOpening()      – lifts the manual open block
//	TriggerForceLiquidate() – forces the engine to close ALL positions next tick
//
// # Feature 3 – Execution Anomaly Protection
//
//	CheckExecution is called for every ExecutionRecord (wired via broker callback).
//	Records are classified as anomalous when:
//	  - Latency > AbnormalLatencyMs (default 500 ms), OR
//	  - FillRate < AbnormalFillRatePct (default 20%) AND status ≠ "FILLED"
//	When AbnormalThreshold anomalous records appear in a rolling window of
//	AbnormalWindowTicks ticks, all trading is halted (TradingStopped = true).
package safety

import (
	"fmt"
	"log"
	"sync"
	"time"

	"astock_trade/core"
)

// ─── Config ───────────────────────────────────────────────────────────────────

// Config holds all tunable parameters for the SafetyGuard.
type Config struct {
	// ─── Feature 1: losing-streak suppression ────────────────────────────────

	// StreakHalfPositionAt: consecutive-loss count that triggers position scaling.
	// 0 uses default (10).
	StreakHalfPositionAt int

	// StreakPositionScale: position scale applied after StreakHalfPositionAt losses.
	// 0 uses default (0.5).
	StreakPositionScale float64

	// StreakFreezeAt: consecutive-loss count that freezes new openings.
	// 0 uses default (15).
	StreakFreezeAt int

	// StreakFreezeTicks: how many ticks to block new BUYs when StreakFreezeAt fires.
	// 0 uses default (12).
	StreakFreezeTicks int

	// BaseMaxTotalPct: the "healthy" MaxTotalPct to restore after streak recovery.
	// Must match PortfolioManager.MaxTotalPct. 0 uses default (0.80).
	BaseMaxTotalPct float64

	// ─── Feature 3: execution anomaly detection ──────────────────────────────

	// AbnormalLatencyMs: executions with Latency > this value are anomalous.
	// 0 uses default (500 ms).
	AbnormalLatencyMs int64

	// AbnormalFillRatePct: executions with FillRate < this value (and not FILLED)
	// are anomalous. 0 uses default (20.0 %).
	AbnormalFillRatePct float64

	// AbnormalWindowTicks: rolling tick window for anomaly counting.
	// 0 uses default (10).
	AbnormalWindowTicks int

	// AbnormalThreshold: number of anomalous executions in the window that
	// halts all trading. 0 uses default (3).
	AbnormalThreshold int

	// ─── Reporting ───────────────────────────────────────────────────────────

	// StatusEveryNTicks: how often to print the safety status line. 0 → 10.
	StatusEveryNTicks int
}

// defaults fills zero-valued fields with production-grade defaults.
func defaults(c Config) Config {
	if c.StreakHalfPositionAt <= 0 {
		c.StreakHalfPositionAt = 10
	}
	if c.StreakPositionScale <= 0 || c.StreakPositionScale > 1 {
		c.StreakPositionScale = 0.5
	}
	if c.StreakFreezeAt <= 0 {
		c.StreakFreezeAt = 15
	}
	if c.StreakFreezeTicks <= 0 {
		c.StreakFreezeTicks = 12
	}
	if c.BaseMaxTotalPct <= 0 {
		c.BaseMaxTotalPct = 0.80
	}
	if c.AbnormalLatencyMs <= 0 {
		c.AbnormalLatencyMs = 500
	}
	if c.AbnormalFillRatePct <= 0 {
		c.AbnormalFillRatePct = 20.0
	}
	if c.AbnormalWindowTicks <= 0 {
		c.AbnormalWindowTicks = 10
	}
	if c.AbnormalThreshold <= 0 {
		c.AbnormalThreshold = 3
	}
	if c.StatusEveryNTicks <= 0 {
		c.StatusEveryNTicks = 10
	}
	return c
}

// Default returns a Config with production-grade defaults.
func Default() Config {
	return defaults(Config{})
}

// ─── Guard ────────────────────────────────────────────────────────────────────

// Guard implements core.SafetyGuard. Thread-safe.
type Guard struct {
	mu  sync.Mutex
	cfg Config

	portMgr core.MaxTotalPctSetter // nil → no runtime MaxTotalPct adjustment

	// Feature 1: losing-streak state
	currentStreak int
	frozenTicks   int // remaining ticks in streak-freeze; 0 = not frozen
	streakScale   float64

	// Feature 2: operator manual controls
	manualStopOpen  bool
	forceLiqPending bool

	// Feature 3: execution anomaly detection
	abnormalWindow []int64 // ring buffer: tick numbers of anomalous executions
	tradingStopped bool

	// Tick counter (mirrors engine tick count for window expiry)
	tick int

	// Periodic status output
	statusTickCount int
}

// New creates a Guard with the provided Config.
// portMgr must implement core.MaxTotalPctSetter (e.g. portfolio.Manager).
// Pass nil to disable runtime MaxTotalPct adjustment (manual mode only).
func New(cfg Config, portMgr core.MaxTotalPctSetter) *Guard {
	cfg = defaults(cfg)
	return &Guard{
		cfg:         cfg,
		portMgr:     portMgr,
		streakScale: 1.0,
	}
}

// Compile-time interface check.
var _ core.SafetyGuard = (*Guard)(nil)

// ─── core.SafetyGuard implementation ─────────────────────────────────────────

// AdvanceTick must be called once per engine tick before any other guard checks.
// It decrements the streak freeze countdown and re-applies MaxTotalPct scaling.
func (g *Guard) AdvanceTick() {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.tick++
	g.statusTickCount++

	if g.frozenTicks > 0 {
		g.frozenTicks--
		if g.frozenTicks == 0 {
			log.Printf("  ✅ [SafetyGuard] 连续亏损冻结解除  streak=%d  tick=%d",
				g.currentStreak, g.tick)
		}
	}

	g.applyStreakScale()

	if g.statusTickCount >= g.cfg.StatusEveryNTicks {
		g.statusTickCount = 0
		g.printStatus()
	}
}

// OnTradeClosed updates the losing-streak counter after every confirmed SELL.
// pnlPct < 0 increments the streak; pnlPct ≥ 0 resets it.
func (g *Guard) OnTradeClosed(pnlPct float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if pnlPct < 0 {
		g.currentStreak++
		log.Printf("  ⚠️  [SafetyGuard] 连续亏损  streak=%d  pnl=%.2f%%  tick=%d",
			g.currentStreak, pnlPct, g.tick)

		switch {
		case g.currentStreak >= g.cfg.StreakFreezeAt && g.frozenTicks == 0:
			g.frozenTicks = g.cfg.StreakFreezeTicks
			log.Printf("  🚫 [SafetyGuard] 连续亏损 %d 笔 → 停止开仓 %d tick  tick=%d",
				g.currentStreak, g.cfg.StreakFreezeTicks, g.tick)

		case g.currentStreak >= g.cfg.StreakHalfPositionAt:
			if g.streakScale > g.cfg.StreakPositionScale {
				log.Printf("  📉 [SafetyGuard] 连续亏损 %d 笔 → 仓位上限 ×%.2f  tick=%d",
					g.currentStreak, g.cfg.StreakPositionScale, g.tick)
			}
		}
		g.applyStreakScale()
	} else {
		if g.currentStreak > 0 {
			log.Printf("  ✅ [SafetyGuard] 亏损连streak重置  (streak was %d)  pnl=%.2f%%",
				g.currentStreak, pnlPct)
		}
		g.currentStreak = 0
		g.applyStreakScale()
	}
}

// AllowOpen returns true when new BUY orders are permitted.
// Returns false (with a log line) under any of the following conditions:
//   - Streak-based freeze is active (frozenTicks > 0)
//   - Manual stop is in effect (manualStopOpen = true)
//   - Trading has been halted due to execution anomalies (tradingStopped = true)
func (g *Guard) AllowOpen() bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.tradingStopped {
		return false
	}
	if g.manualStopOpen {
		return false
	}
	if g.frozenTicks > 0 {
		return false
	}
	return true
}

// ShouldForceLiquidate returns true when a force-liquidate has been triggered
// and is awaiting acknowledgement. True for at most one call per trigger.
func (g *Guard) ShouldForceLiquidate() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.forceLiqPending
}

// AcknowledgeForceLiquidate clears the force-liquidate flag.
// The engine must call this after it has submitted all liquidation orders.
func (g *Guard) AcknowledgeForceLiquidate() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.forceLiqPending = false
	log.Printf("  ✅ [SafetyGuard] 强平已确认执行  tick=%d", g.tick)
}

// ── Operator manual controls ──────────────────────────────────────────────────

// StopOpening prevents new position openings until ResumeOpening is called.
// Safe to call from any goroutine (e.g. HTTP handler, signal handler).
func (g *Guard) StopOpening() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.manualStopOpen = true
	log.Printf("  🛑 [SafetyGuard] 人工指令：禁止开仓  tick=%d  time=%s",
		g.tick, time.Now().Format("15:04:05"))
}

// ResumeOpening lifts the manual open block set by StopOpening.
// Operator intent here is "resume trading now", so it also clears the
// anomaly-triggered trading halt and resets the abnormal execution window.
func (g *Guard) ResumeOpening() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.manualStopOpen = false
	clearedAbnormal := g.tradingStopped || len(g.abnormalWindow) > 0
	g.tradingStopped = false
	g.abnormalWindow = g.abnormalWindow[:0]
	if clearedAbnormal {
		log.Printf("  ▶️  [SafetyGuard] 人工指令：恢复开仓并清除异常暂停  tick=%d  time=%s",
			g.tick, time.Now().Format("15:04:05"))
		return
	}
	log.Printf("  ▶️  [SafetyGuard] 人工指令：恢复开仓  tick=%d  time=%s",
		g.tick, time.Now().Format("15:04:05"))
}

// TriggerForceLiquidate signals the engine to close all open positions on the
// next tick.  The engine checks ShouldForceLiquidate() each tick.
// Safe to call from any goroutine.
func (g *Guard) TriggerForceLiquidate() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.forceLiqPending = true
	log.Printf("  🚨 [SafetyGuard] 人工指令：全部清仓  tick=%d  time=%s",
		g.tick, time.Now().Format("15:04:05"))
}

// ── Anomaly detection ─────────────────────────────────────────────────────────

// CheckExecution analyses one ExecutionRecord for anomalies.
// Called by the broker's logger callback for every execution attempt.
//
// An execution is classified as anomalous when:
//   - Latency > AbnormalLatencyMs, OR
//   - Status ≠ "FILLED" AND FillRate < AbnormalFillRatePct
//
// When the number of anomalous ticks in the rolling window reaches
// AbnormalThreshold, TradingStopped is set to true and all trading halts.
func (g *Guard) CheckExecution(rec *core.ExecutionRecord) {
	if rec == nil {
		return
	}

	latencyAbnormal := rec.Latency > g.cfg.AbnormalLatencyMs
	fillAbnormal := rec.Status != "FILLED" && rec.FillRate < g.cfg.AbnormalFillRatePct

	if !latencyAbnormal && !fillAbnormal {
		return
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	reason := ""
	if latencyAbnormal {
		reason += fmt.Sprintf("延迟异常 %dms>%dms", rec.Latency, g.cfg.AbnormalLatencyMs)
	}
	if fillAbnormal {
		if reason != "" {
			reason += " | "
		}
		reason += fmt.Sprintf("成交率异常 %.0f%%<%0.f%%",
			rec.FillRate, g.cfg.AbnormalFillRatePct)
	}
	log.Printf("  ⚡ [SafetyGuard] 执行异常  %s  %s  orderID=%s",
		rec.Symbol, reason, rec.OrderID)

	// Append current tick to abnormal window; purge ticks outside window.
	g.abnormalWindow = append(g.abnormalWindow, int64(g.tick))
	cutoff := int64(g.tick - g.cfg.AbnormalWindowTicks)
	filtered := g.abnormalWindow[:0]
	for _, t := range g.abnormalWindow {
		if t >= cutoff {
			filtered = append(filtered, t)
		}
	}
	g.abnormalWindow = filtered

	if !g.tradingStopped && len(g.abnormalWindow) >= g.cfg.AbnormalThreshold {
		g.tradingStopped = true
		log.Printf("  🚨 [SafetyGuard] 执行异常次数过多（%d/%d ticks）→ 暂停所有交易  tick=%d",
			len(g.abnormalWindow), g.cfg.AbnormalWindowTicks, g.tick)
	}
}

// ── Inspection ────────────────────────────────────────────────────────────────

// SafetyStatus returns a snapshot of the current guard state.
func (g *Guard) SafetyStatus() core.SafetyStatus {
	g.mu.Lock()
	defer g.mu.Unlock()
	return core.SafetyStatus{
		CurrentStreak:   g.currentStreak,
		FreezeTicksLeft: g.frozenTicks,
		StreakScale:     g.streakScale,
		ManualStopOpen:  g.manualStopOpen,
		ForceLiqPending: g.forceLiqPending,
		AbnormalCount:   len(g.abnormalWindow),
		TradingStopped:  g.tradingStopped,
	}
}

// ResetTradingStopped clears the trading-stopped flag set by anomaly detection.
// Only call after verifying the underlying execution issue has been resolved.
func (g *Guard) ResetTradingStopped() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tradingStopped = false
	g.abnormalWindow = g.abnormalWindow[:0]
	log.Printf("  ✅ [SafetyGuard] 交易异常标志已重置  tick=%d  time=%s",
		g.tick, time.Now().Format("15:04:05"))
}

// CurrentStreak returns the current consecutive-loss streak count.
// Thread-safe; intended for external monitoring dashboards.
func (g *Guard) CurrentStreak() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.currentStreak
}

// Restore seeds the guard with persisted status before live ticks resume.
// It is best-effort: fields not present in the persisted snapshot keep defaults.
func (g *Guard) Restore(currentStreak int, abnormalCount int, allowOpen bool, tradingStopped bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.currentStreak = currentStreak
	g.tradingStopped = tradingStopped
	if abnormalCount < 0 {
		abnormalCount = 0
	}
	g.abnormalWindow = make([]int64, abnormalCount)
	for i := range g.abnormalWindow {
		g.abnormalWindow[i] = int64(g.tick)
	}
	if !allowOpen && !tradingStopped && currentStreak < g.cfg.StreakFreezeAt {
		g.manualStopOpen = true
	}
	g.applyStreakScale()
}

// ── Internal helpers ──────────────────────────────────────────────────────────

// applyStreakScale recomputes streakScale from currentStreak + frozenTicks
// and propagates the change to portMgr if available.
// Must be called with g.mu held.
func (g *Guard) applyStreakScale() {
	var scale float64
	switch {
	case g.frozenTicks > 0:
		scale = 0.0 // frozen: no new opens AND MaxTotalPct→0 so existing alloc also shrinks
	case g.currentStreak >= g.cfg.StreakHalfPositionAt:
		scale = g.cfg.StreakPositionScale
	default:
		scale = 1.0
	}

	if scale == g.streakScale {
		return
	}
	g.streakScale = scale

	if g.portMgr != nil {
		effective := g.cfg.BaseMaxTotalPct * scale
		g.portMgr.SetMaxTotalPct(effective)
		log.Printf("  📊 [SafetyGuard] MaxTotalPct 调整 → %.0f%%  (base=%.0f%% × streakScale=%.1f)  tick=%d",
			effective*100, g.cfg.BaseMaxTotalPct*100, scale, g.tick)
	}
}

// printStatus prints a one-line status dashboard.
// Must be called with g.mu held.
func (g *Guard) printStatus() {
	openStatus := "允许开仓"
	if g.tradingStopped {
		openStatus = "🚨交易暂停(异常)"
	} else if g.manualStopOpen {
		openStatus = "🛑人工禁止"
	} else if g.frozenTicks > 0 {
		openStatus = fmt.Sprintf("🚫冻结%d tick", g.frozenTicks)
	}

	scaleStr := "正常(1.0×)"
	if g.streakScale < 1.0 {
		scaleStr = fmt.Sprintf("%.1f×", g.streakScale)
	}

	log.Printf(
		"[SafetyGuard|Tick%d]  连续亏损=%d笔  仓位=%s  开仓=%s  异常执行=%d",
		g.tick, g.currentStreak, scaleStr, openStatus, len(g.abnormalWindow),
	)
}
