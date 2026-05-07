// Package monitor provides a real-time portfolio health monitor that tracks
// equity, drawdown, and risk levels, and fires alert callbacks when risk
// escalates.
//
// Risk level classification:
//
//	NORMAL    – drawdown < CautionPct (default 3%)
//	CAUTION   – drawdown ≥ 3%; tighten position sizing
//	DEFENSE   – drawdown ≥ 5%; AdaptiveOptimizer should reduce MaxTotalPct
//	EMERGENCY – drawdown ≥ 8%; Kill Switch threshold; halt new BUYs
//
// The Monitor implements core.Monitor so it can be attached to the engine
// via Engine.SetMonitor.
package monitor

import (
	"fmt"
	"log"
	"sync"
	"time"

	"astock_trade/core"
)

// Config holds monitoring thresholds.
type Config struct {
	CautionDrawdownPct   float64 // default 3.0 (%)
	DefenseDrawdownPct   float64 // default 5.0 (%)
	EmergencyDrawdownPct float64 // default 8.0 (%) – Kill Switch level
	ReportEveryNTicks    int     // default 10; how often to print the status line
}

// Monitor satisfies core.Monitor and adds alert-registration and state-query APIs.
type Monitor struct {
	cfg       Config
	mu        sync.RWMutex
	state     core.MonitorState
	alertFns  []func(core.AlertEvent)
	tickCnt   int
	lastLevel core.RiskLevel

	// safetyGuard is optional; when set, its status is included in the periodic
	// status line for a single-pane-of-glass operational view.
	safetyGuard core.SafetyGuard
}

// New returns a Monitor with the provided Config.
// Zero fields are replaced by sensible defaults.
func New(cfg Config) *Monitor {
	if cfg.CautionDrawdownPct == 0 {
		cfg.CautionDrawdownPct = 3.0
	}
	if cfg.DefenseDrawdownPct == 0 {
		cfg.DefenseDrawdownPct = 5.0
	}
	if cfg.EmergencyDrawdownPct == 0 {
		cfg.EmergencyDrawdownPct = 8.0
	}
	if cfg.ReportEveryNTicks == 0 {
		cfg.ReportEveryNTicks = 10
	}
	return &Monitor{cfg: cfg}
}

// OnAlert registers a callback that is called synchronously whenever the
// risk level escalates (NORMAL→CAUTION, CAUTION→DEFENSE, etc.).
// Multiple callbacks may be registered; they are called in registration order.
func (m *Monitor) OnAlert(f func(core.AlertEvent)) {
	m.mu.Lock()
	m.alertFns = append(m.alertFns, f)
	m.mu.Unlock()
}

// SetSafetyGuard attaches an optional SafetyGuard whose status is included
// in the periodic status line printed by the monitor.
// Must be called before Run / the first Update tick.
func (m *Monitor) SetSafetyGuard(g core.SafetyGuard) {
	m.mu.Lock()
	m.safetyGuard = g
	m.mu.Unlock()
}

// Update implements core.Monitor.
// Called every engine tick at the end of Phase 5.
func (m *Monitor) Update(equity float64, report core.PerformanceReport, positions []core.Position) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tickCnt++

	peak := m.state.PeakEquity
	if equity > peak {
		peak = equity
	}

	drawdown := 0.0
	if report.InitialCapital > 0 && equity < report.InitialCapital {
		drawdown = (report.InitialCapital - equity) / report.InitialCapital * 100
	}

	level := core.RiskNormal
	switch {
	case drawdown >= m.cfg.EmergencyDrawdownPct:
		level = core.RiskEmergency
	case drawdown >= m.cfg.DefenseDrawdownPct:
		level = core.RiskDefense
	case drawdown >= m.cfg.CautionDrawdownPct:
		level = core.RiskCaution
	}

	posCopy := make([]core.Position, len(positions))
	copy(posCopy, positions)

	m.state = core.MonitorState{
		Timestamp:   time.Now().UnixMilli(),
		Equity:      equity,
		PeakEquity:  peak,
		DrawdownPct: drawdown,
		RiskLevel:   level,
		Positions:   posCopy,
		TradeCount:  report.TradeCount,
		WinRate:     report.WinRate,
	}

	// Fire alert callbacks when risk level escalates.
	if level > m.lastLevel {
		event := core.AlertEvent{
			Level: level,
			Message: fmt.Sprintf(
				"[Monitor] 风险档位升级 %s → %s  回撤=%.2f%%  权益=¥%.0f",
				m.lastLevel, level, drawdown, equity),
			Timestamp: m.state.Timestamp,
			Equity:    equity,
			Drawdown:  drawdown,
		}
		icon := riskIcon(level)
		log.Printf("%s %s", icon, event.Message)
		for _, fn := range m.alertFns {
			fn(event)
		}
	}
	// Alert on recovery (de-escalation) too – informational only.
	if level < m.lastLevel {
		log.Printf("✅ [Monitor] 风险档位恢复 %s → %s  回撤=%.2f%%  权益=¥%.0f",
			m.lastLevel, level, drawdown, equity)
	}

	m.lastLevel = level

	// Periodic status log.
	if m.tickCnt%m.cfg.ReportEveryNTicks == 0 {
		m.printStatus()
	}
}

// State implements core.Monitor.
func (m *Monitor) State() core.MonitorState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// Restore seeds the monitor with persisted portfolio state before live ticks resume.
func (m *Monitor) Restore(state core.MonitorState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = state
	m.lastLevel = state.RiskLevel
}

// printStatus prints a one-line dashboard to the standard logger.
// Must be called with m.mu held.
func (m *Monitor) printStatus() {
	s := m.state
	icon := riskIcon(s.RiskLevel)

	// Base portfolio status line.
	line := fmt.Sprintf(
		"%s [Monitor|Tick%d]  权益=¥%.0f  峰值=¥%.0f  回撤=%.2f%%  档位=%-9s  持仓=%d  胜率=%.1f%%  交易=%d",
		icon, m.tickCnt,
		s.Equity, s.PeakEquity, s.DrawdownPct, s.RiskLevel,
		len(s.Positions), s.WinRate, s.TradeCount,
	)
	log.Println(line)

	// Safety guard status line (when attached).
	if m.safetyGuard != nil {
		st := m.safetyGuard.SafetyStatus()
		openStatus := "✅开仓正常"
		if st.TradingStopped {
			openStatus = "🚨交易暂停(异常)"
		} else if st.ManualStopOpen {
			openStatus = "🛑人工禁止"
		} else if st.FreezeTicksLeft > 0 {
			openStatus = fmt.Sprintf("🚫冻结%dtick", st.FreezeTicksLeft)
		}
		scaleStr := "1.0×"
		if st.StreakScale < 1.0 {
			scaleStr = fmt.Sprintf("%.1f×", st.StreakScale)
		}
		log.Printf(
			"  [SafetyStatus]  连续亏损=%d笔  仓位=%s  %s  异常执行=%d  强平待触发=%v",
			st.CurrentStreak, scaleStr, openStatus, st.AbnormalCount, st.ForceLiqPending,
		)
	}
}

// PrintFinalReport prints a comprehensive end-of-run monitoring summary.
func (m *Monitor) PrintFinalReport() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.state
	icon := riskIcon(s.RiskLevel)
	log.Println("══════════════════════════════════════════════════════════════")
	log.Println("  监控报告 (Monitor Final Report)")
	log.Println("══════════════════════════════════════════════════════════════")
	log.Printf("  最终权益:   ¥%.2f", s.Equity)
	log.Printf("  峰值权益:   ¥%.2f", s.PeakEquity)
	log.Printf("  当前回撤:   %.2f%%", s.DrawdownPct)
	log.Printf("  风险档位:   %s %s", icon, s.RiskLevel)
	log.Printf("  总 Tick 数: %d", m.tickCnt)
	log.Printf("  总交易次数: %d", s.TradeCount)
	log.Printf("  胜率:       %.1f%%", s.WinRate)
	if m.safetyGuard != nil {
		st := m.safetyGuard.SafetyStatus()
		log.Println("──────────────────────────────────────────────────────────────")
		log.Printf("  [安全控制层]")
		log.Printf("  连续亏损:   %d 笔", st.CurrentStreak)
		log.Printf("  仓位倍数:   %.1f×", st.StreakScale)
		log.Printf("  人工禁止:   %v", st.ManualStopOpen)
		log.Printf("  交易暂停:   %v (异常执行=%d)", st.TradingStopped, st.AbnormalCount)
	}
	log.Println("══════════════════════════════════════════════════════════════")
}

func riskIcon(level core.RiskLevel) string {
	switch level {
	case core.RiskNormal:
		return "✅"
	case core.RiskCaution:
		return "⚡"
	case core.RiskDefense:
		return "🛡️"
	case core.RiskEmergency:
		return "🚨"
	default:
		return "❓"
	}
}
