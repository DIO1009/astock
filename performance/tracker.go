// Package performance implements PerformanceTracker: trade recording, equity
// curve maintenance, core strategy metrics, and periodic formatted reports.
//
// All metrics are computed from first principles on each call to Report()
// so they always reflect the current state of the trade history.
package performance

import (
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"astock_trade/core"
)

// Config holds tracker configuration.
type Config struct {
	// InitialCapital is the starting cash balance.
	InitialCapital float64

	// ReportEveryNTicks controls how often MaybeReport prints a summary.
	// 0 disables automatic reporting.
	ReportEveryNTicks int
}

// Tracker satisfies core.PerformanceTracker.
type Tracker struct {
	mu             sync.Mutex
	cfg            Config
	cash           float64           // actual cash after all buy/sell activity
	closedTrades   []core.ClosedTrade
	equityCurve    []float64         // one entry per tick
	lastReportTick int
}

// New creates a Tracker initialised with the provided configuration.
func New(cfg Config) *Tracker {
	return &Tracker{
		cfg:  cfg,
		cash: cfg.InitialCapital,
	}
}

// ── core.PerformanceTracker implementation ────────────────────────────────────

// SeedRestoredPositions deducts the cost of positions restored from a snapshot
// from the tracked cash balance.  Call this once at startup after LoadState,
// before any ticks run.
//
// Without this the tracker starts with full InitialCapital but never records
// the BUY trades that created the restored positions, so when those positions
// are later sold the proceeds are credited on top of the full capital,
// inflating totalReturn and cash by the entire position value.
func (t *Tracker) SeedRestoredPositions(positions []core.Position) {
	if len(positions) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, p := range positions {
		t.cash -= p.AvgPrice * float64(p.Quantity)
	}
}

// OnBuy deducts the trade cost from the cash balance.
func (t *Tracker) OnBuy(trade *core.Trade) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cash -= trade.Price * float64(trade.Quantity)
}

// OnSell credits proceeds, records the closed trade, and logs a per-trade
// equity snapshot.
func (t *Tracker) OnSell(trade *core.Trade, entryAvgPrice float64, holdTicks int, exitType string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	proceeds := trade.Price * float64(trade.Quantity)
	t.cash += proceeds

	pnlPct := 0.0
	if entryAvgPrice > 0 {
		pnlPct = (trade.Price - entryAvgPrice) / entryAvgPrice * 100
	}

	t.closedTrades = append(t.closedTrades, core.ClosedTrade{
		Symbol:     trade.Symbol,
		EntryPrice: entryAvgPrice,
		ExitPrice:  trade.Price,
		Quantity:   trade.Quantity,
		PnlPct:     pnlPct,
		HoldTicks:  holdTicks,
		ExitReason: exitType,
		Timestamp:  trade.Timestamp,
	})

	// Inline equity snapshot for the log line (equity = cash at this moment,
	// positions may still be open – logged positions will update next RecordEquity).
	totalReturn := (t.cash - t.cfg.InitialCapital) / t.cfg.InitialCapital * 100
	dd := maxDrawdown(t.equityCurve)
	log.Printf("  📊 [Perf] cash=¥%9.0f  totalReturn=%+.2f%%  drawdown=%.2f%%  pnl=%+.2f%%  hold=%dtick  exit=%s",
		t.cash, totalReturn, dd, pnlPct, holdTicks, exitType)
}

// RecordEquity appends the current equity to the historical curve.
func (t *Tracker) RecordEquity(equity float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.equityCurve = append(t.equityCurve, equity)
}

// MaybeReport prints the periodic summary report if enough ticks have elapsed.
func (t *Tracker) MaybeReport(tick int) {
	if t.cfg.ReportEveryNTicks <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if tick-t.lastReportTick < t.cfg.ReportEveryNTicks {
		return
	}
	t.lastReportTick = tick
	r := t.computeReport()
	t.printReport(r, tick)
}

// Report computes and returns current performance metrics (safe to call any time).
func (t *Tracker) Report() core.PerformanceReport {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.computeReport()
}

// Cash returns the current tracked cash balance.
func (t *Tracker) Cash() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cash
}

// ClosedTrades returns a snapshot copy of all recorded closed trades (newest last).
func (t *Tracker) ClosedTrades() []core.ClosedTrade {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]core.ClosedTrade, len(t.closedTrades))
	copy(out, t.closedTrades)
	return out
}

// Restore replaces the tracker's runtime state from persisted history.
// It is intended for startup hydration before the engine begins ticking.
func (t *Tracker) Restore(cash float64, equityCurve []float64, closedTrades []core.ClosedTrade) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cash = cash
	t.equityCurve = append(t.equityCurve[:0], equityCurve...)
	t.closedTrades = append(t.closedTrades[:0], closedTrades...)
	if n := len(t.equityCurve); n > 0 {
		t.lastReportTick = n
	}
}

// ── internal helpers ──────────────────────────────────────────────────────────

// computeReport derives all metrics; must be called under t.mu.
func (t *Tracker) computeReport() core.PerformanceReport {
	r := core.PerformanceReport{
		TickCount:      len(t.equityCurve),
		InitialCapital: t.cfg.InitialCapital,
	}

	// Current equity
	if n := len(t.equityCurve); n > 0 {
		r.CurrentEquity = t.equityCurve[n-1]
	} else {
		r.CurrentEquity = t.cash
	}

	r.TotalReturn = (r.CurrentEquity - r.InitialCapital) / r.InitialCapital * 100
	r.MaxDrawdown = maxDrawdown(t.equityCurve)

	// Trade metrics
	var totalWinPct, totalLossPct, totalHoldTicks float64
	for _, ct := range t.closedTrades {
		r.TradeCount++
		totalHoldTicks += float64(ct.HoldTicks)
		if ct.PnlPct > 0 {
			r.WinCount++
			totalWinPct += ct.PnlPct
		} else {
			r.LossCount++
			totalLossPct += math.Abs(ct.PnlPct)
		}
		switch ct.ExitReason {
		case "STOP_LOSS":
			r.StopLossCount++
		case "TAKE_PROFIT":
			r.TakeProfitCount++
		case "TRAIL_STOP":
			r.TrailStopCount++
		}
	}

	if r.TradeCount > 0 {
		r.AvgHoldTicks = totalHoldTicks / float64(r.TradeCount)
	}
	if r.WinCount > 0 {
		r.WinRate = float64(r.WinCount) / float64(r.TradeCount) * 100
		r.AvgWin = totalWinPct / float64(r.WinCount)
	}
	if r.LossCount > 0 {
		r.AvgLoss = totalLossPct / float64(r.LossCount)
	}
	if totalLossPct > 0 {
		r.ProfitFactor = totalWinPct / totalLossPct
	} else if totalWinPct > 0 {
		r.ProfitFactor = 999 // all wins, no losses
	}

	// ── Feature 5: Enhanced statistics ────────────────────────────────────

	// MaxConsecutiveLoss & MaxConsecutiveLossPct
	curStreak, curStreakPct := 0, 0.0
	for _, ct := range t.closedTrades {
		if ct.PnlPct < 0 {
			curStreak++
			curStreakPct += ct.PnlPct
			if curStreak > r.MaxConsecutiveLoss {
				r.MaxConsecutiveLoss = curStreak
				r.MaxConsecutiveLossPct = curStreakPct
			}
		} else {
			curStreak, curStreakPct = 0, 0.0
		}
	}

	// Top5PnlConcentration: top-5 wins / total gross winning PnL
	var winPnls []float64
	for _, ct := range t.closedTrades {
		if ct.PnlPct > 0 {
			winPnls = append(winPnls, ct.PnlPct)
		}
	}
	if len(winPnls) > 0 && totalWinPct > 0 {
		sort.Sort(sort.Reverse(sort.Float64Slice(winPnls)))
		top5 := 0.0
		for i := 0; i < len(winPnls) && i < 5; i++ {
			top5 += winPnls[i]
		}
		r.Top5PnlConcentration = top5 / totalWinPct * 100
	}

	// SharpeProxy from equity curve tick returns
	r.SharpeProxy = sharpeProxy(t.equityCurve)

	return r
}

// maxDrawdown computes the peak-to-trough drawdown (%) over the equity curve.
func maxDrawdown(curve []float64) float64 {
	if len(curve) < 2 {
		return 0
	}
	peak := curve[0]
	dd := 0.0
	for _, v := range curve {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			d := (peak - v) / peak * 100
			if d > dd {
				dd = d
			}
		}
	}
	return dd
}

// sharpeProxy computes a simplified annualised Sharpe-like ratio from the
// equity curve.  Uses per-tick returns; annualises assuming 252 trading ticks.
// Returns 0 when the curve is too short or has zero return variance.
func sharpeProxy(curve []float64) float64 {
	n := len(curve)
	if n < 3 {
		return 0
	}
	returns := make([]float64, n-1)
	for i := 1; i < n; i++ {
		if curve[i-1] > 0 {
			returns[i-1] = (curve[i] - curve[i-1]) / curve[i-1]
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
	std := math.Sqrt(variance)
	if std == 0 {
		return 0
	}
	return mean / std * math.Sqrt(252)
}

// printReport formats and logs the weekly summary; must be called under t.mu.
func (t *Tracker) printReport(r core.PerformanceReport, tick int) {
	pfStr := fmt.Sprintf("%.2f", r.ProfitFactor)
	if r.ProfitFactor >= 999 {
		pfStr = "∞ (no losses)"
	}
	breakdown := fmt.Sprintf("SL=%d  TP=%d  TRAIL=%d",
		r.StopLossCount, r.TakeProfitCount, r.TrailStopCount)
	winLoss := fmt.Sprintf("%d盈 / %d亏", r.WinCount, r.LossCount)
	sparkline := buildSparkline(t.equityCurve, 40)

	// Feature 5 strings
	consStr := fmt.Sprintf("连亏最长  %d笔 (%.2f%%)", r.MaxConsecutiveLoss, r.MaxConsecutiveLossPct)
	concStr := "n/a"
	if r.WinCount > 0 {
		concStr = fmt.Sprintf("Top5盈利集中度 %.1f%%", r.Top5PnlConcentration)
	}
	sharpeStr := "n/a"
	if r.SharpeProxy != 0 {
		sharpeStr = fmt.Sprintf("%.2f", r.SharpeProxy)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"\n╔══ 📈 Weekly Report  tick=%-3d  %s ════════════════════════════\n",
		tick, time.Now().Format("15:04:05")))
	b.WriteString(fmt.Sprintf(
		"║  总收益率   %+8.2f%%   │  最大回撤   %7.2f%%\n",
		r.TotalReturn, r.MaxDrawdown))
	b.WriteString(fmt.Sprintf(
		"║  胜率       %8.1f%%   │  盈亏比     %s\n",
		r.WinRate, pfStr))
	b.WriteString(fmt.Sprintf(
		"║  平均盈利   %+8.2f%%   │  平均亏损   %7.2f%%\n",
		r.AvgWin, r.AvgLoss))
	b.WriteString(fmt.Sprintf(
		"║  交易次数   %8d    │  平均持仓   %.1f tick\n",
		r.TradeCount, r.AvgHoldTicks))
	b.WriteString(fmt.Sprintf(
		"║  盈亏分布   %-12s   │  出场原因   %s\n",
		winLoss, breakdown))
	b.WriteString(fmt.Sprintf(
		"║  %-28s   │  %s\n", consStr, concStr))
	b.WriteString(fmt.Sprintf(
		"║  Sharpe代理  %-8s           │  权益曲线↓\n", sharpeStr))
	b.WriteString(fmt.Sprintf(
		"║  期初资金   ¥%9.0f   │  当前权益   ¥%9.0f\n",
		r.InitialCapital, r.CurrentEquity))
	b.WriteString(fmt.Sprintf(
		"║  权益曲线   %s\n", sparkline))
	b.WriteString("╚═════════════════════════════════════════════════════════════")
	log.Println(b.String())
}

// buildSparkline builds an ASCII bar chart of the equity curve normalised to
// 8 levels (block characters ▁▂▃▄▅▆▇█). Width is capped at maxWidth chars.
func buildSparkline(curve []float64, maxWidth int) string {
	if len(curve) == 0 {
		return "(no data)"
	}
	// Downsample if curve is longer than maxWidth
	step := 1
	if len(curve) > maxWidth {
		step = len(curve) / maxWidth
	}
	var sampled []float64
	for i := 0; i < len(curve); i += step {
		sampled = append(sampled, curve[i])
	}

	minV, maxV := sampled[0], sampled[0]
	for _, v := range sampled {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	rng := maxV - minV
	var sb strings.Builder
	for _, v := range sampled {
		idx := 0
		if rng > 0 {
			idx = int((v - minV) / rng * float64(len(bars)-1))
			if idx >= len(bars) {
				idx = len(bars) - 1
			}
		}
		sb.WriteRune(bars[idx])
	}
	return sb.String()
}
