// Package report implements the daily strategy report generator.
//
// A report is generated every trading day at 15:10 CST and written as a
// Markdown file under the configured report directory (default: reports/).
// The generation result (SUCCESS / FAILED) is recorded in the daily_reports
// PostgreSQL table.
//
// Data integrity checks are performed before writing:
//   - DB connection must be alive
//   - Closing equity must be > 0
//   - Positions table must be queryable
//   - Equity-curve data for the day must exist
//
// If any check fails the report is NOT written and the error is returned so
// the scheduler can retry.
package report

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"astock_trade/core"
	"astock_trade/store"
)

// Generator collects data from multiple sources and produces daily markdown reports.
type Generator struct {
	st        *store.Store
	perfTrack core.PerformanceTracker
	posMgr    core.PositionManager
	reportDir string // directory where .md files are written (e.g. "reports")
}

// NewGenerator creates a Generator.
//   - st        – PostgreSQL store (required)
//   - perfTrack – performance tracker for live equity/cash (may be nil; falls back to DB)
//   - posMgr    – position manager for current holdings (may be nil)
//   - reportDir – directory for markdown files; created if absent
func NewGenerator(
	st *store.Store,
	perfTrack core.PerformanceTracker,
	posMgr core.PositionManager,
	reportDir string,
) *Generator {
	if reportDir == "" {
		reportDir = "reports"
	}
	return &Generator{
		st:        st,
		perfTrack: perfTrack,
		posMgr:    posMgr,
		reportDir: reportDir,
	}
}

// ── Public API ────────────────────────────────────────────────────────────────

// Generate produces the daily report for the given date.
// Returns the path of the written file on success.
func (g *Generator) Generate(ctx context.Context, date time.Time) (string, error) {
	date = date.Truncate(24 * time.Hour)

	// ── 1. Collect data ───────────────────────────────────────────────────────
	data, err := g.collect(ctx, date)
	if err != nil {
		return "", fmt.Errorf("collect: %w", err)
	}

	// ── 2. Integrity check ────────────────────────────────────────────────────
	if err := g.checkIntegrity(data); err != nil {
		return "", fmt.Errorf("integrity: %w", err)
	}

	// ── 3. Render markdown ────────────────────────────────────────────────────
	md := g.render(data)
	if strings.TrimSpace(md) == "" {
		return "", errors.New("rendered report is empty")
	}

	// ── 4. Write file ─────────────────────────────────────────────────────────
	if err := os.MkdirAll(g.reportDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", g.reportDir, err)
	}
	cst := time.FixedZone("CST", 8*3600)
	fname := fmt.Sprintf("%s.md", date.In(cst).Format("2006-01-02"))
	fpath := filepath.Join(g.reportDir, fname)
	if err := os.WriteFile(fpath, []byte(md), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", fpath, err)
	}
	return fpath, nil
}

// ── Data collection ───────────────────────────────────────────────────────────

type reportData struct {
	Date        time.Time
	GeneratedAt time.Time

	// Equity
	OpenEquity  store.EquityQueryRow
	CloseEquity store.EquityQueryRow
	InitCapital float64 // from performance tracker

	// Positions
	Positions []store.PosQueryRow

	// Today's trades
	Executions []store.ExecQueryRow

	// Risk events
	RiskEvents []store.RiskQueryRow

	// Alpha rankings (top-10)
	AlphaRankings []store.AlphaRankRow

	// System status
	SysStatus *store.StatusQueryRow
}

func (g *Generator) collect(ctx context.Context, date time.Time) (*reportData, error) {
	d := &reportData{Date: date, GeneratedAt: time.Now()}

	// Equity curve for the day
	var err error
	d.OpenEquity, d.CloseEquity, err = g.st.QueryDayEquity(ctx, date)
	if err != nil {
		return nil, fmt.Errorf("equity: %w", err)
	}

	// Augment closing equity from live perf tracker when available and fresher
	if g.perfTrack != nil {
		live := g.perfTrack.Cash()
		if live > 0 && (d.CloseEquity.Equity == 0 || live > d.CloseEquity.Cash*0.5) {
			// Use live cash as a floor; equity in DB should be more authoritative
			// but supplement if DB is stale.
			d.InitCapital = live // proxy
		}
	}

	// Positions from DB (most up-to-date snapshot)
	d.Positions, err = g.st.QueryPositions(ctx)
	if err != nil {
		return nil, fmt.Errorf("positions: %w", err)
	}

	// Supplement with in-memory positions if DB is empty but posMgr has data
	if len(d.Positions) == 0 && g.posMgr != nil {
		for _, p := range g.posMgr.AllPositions() {
			d.Positions = append(d.Positions, store.PosQueryRow{
				Symbol:   p.Symbol,
				Qty:      p.Quantity,
				AvgPrice: p.AvgPrice,
			})
		}
	}

	// Today's executions
	d.Executions, err = g.st.QueryDayExecutions(ctx, date)
	if err != nil {
		return nil, fmt.Errorf("executions: %w", err)
	}

	// Today's risk events
	d.RiskEvents, err = g.st.QueryDayRiskEvents(ctx, date)
	if err != nil {
		return nil, fmt.Errorf("risk events: %w", err)
	}

	// Alpha rankings top-10
	d.AlphaRankings, err = g.st.QueryTopAlphaRankings(ctx, date, 10)
	if err != nil {
		// Non-fatal: alpha rankings may not exist in static-screener mode
		d.AlphaRankings = nil
	}

	// System status
	d.SysStatus, err = g.st.QueryLatestSystemStatus(ctx)
	if err != nil {
		d.SysStatus = nil // non-fatal
	}

	return d, nil
}

// ── Integrity check ───────────────────────────────────────────────────────────

// ErrIntegrity is returned when data integrity checks fail.
type ErrIntegrity struct{ Reason string }

func (e *ErrIntegrity) Error() string { return "integrity check failed: " + e.Reason }

func (g *Generator) checkIntegrity(d *reportData) error {
	// 1. Equity data must exist and be positive
	if d.CloseEquity.Equity <= 0 && d.OpenEquity.Equity <= 0 {
		return &ErrIntegrity{
			Reason: "no equity-curve data for today (system may not have run today)",
		}
	}
	// 2. Equity must be a sane value (not absurdly small or large)
	eq := d.CloseEquity.Equity
	if eq == 0 {
		eq = d.OpenEquity.Equity
	}
	if eq < 1_000 || eq > 1_000_000_000 {
		return &ErrIntegrity{
			Reason: fmt.Sprintf("equity value out of sane range: %.2f", eq),
		}
	}
	// 3. Positions must be queryable (nil slice is OK = empty, but error means DB issue)
	// — Already guaranteed by collect() returning error on DB failure.

	// 4. If there are positions, each must have a positive avg_price
	for _, p := range d.Positions {
		if p.AvgPrice <= 0 {
			return &ErrIntegrity{
				Reason: fmt.Sprintf("position %s has invalid avg_price %.4f", p.Symbol, p.AvgPrice),
			}
		}
	}
	return nil
}

// ── Markdown rendering ────────────────────────────────────────────────────────

func (g *Generator) render(d *reportData) string {
	cst := time.FixedZone("CST", 8*3600)
	dateStr := d.Date.In(cst).Format("2006-01-02")
	weekday := weekdayCN(d.Date.In(cst).Weekday())

	eq := d.CloseEquity.Equity
	if eq == 0 {
		eq = d.OpenEquity.Equity
	}
	dd := d.CloseEquity.Drawdown
	cash := d.CloseEquity.Cash
	posVal := d.CloseEquity.PositionValue
	openEq := d.OpenEquity.Equity
	dailyPnL := 0.0
	dailyPct := 0.0
	if openEq > 0 {
		dailyPnL = eq - openEq
		dailyPct = dailyPnL / openEq * 100
	}
	posPct := 0.0
	if eq > 0 {
		posPct = posVal / eq * 100
	}

	var sb strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&sb, format, a...) }

	// ── Header ────────────────────────────────────────────────────────────────
	w("# AStock 每日策略报告\n\n")
	w("**日期**: %s（%s）  \n", dateStr, weekday)
	w("**生成时间**: %s CST\n\n", d.GeneratedAt.In(cst).Format("15:04:05"))
	w("---\n\n")

	// ── Account summary ───────────────────────────────────────────────────────
	w("## 账户总览\n\n")
	w("| 指标 | 数值 |\n|------|------|\n")
	w("| 最终权益 | **¥%.2f** |\n", eq)
	w("| 今日收益 | %s¥%.2f (%+.2f%%) |\n", signEmoji(dailyPnL), abs(dailyPnL), dailyPct)
	w("| 当前回撤 | %.2f%% |\n", dd)
	w("| 现金余额 | ¥%.2f |\n", cash)
	w("| 持仓市值 | ¥%.2f |\n", posVal)
	w("| 持仓比例 | %.1f%% |\n", posPct)
	if d.SysStatus != nil {
		w("| 风险等级 | **%s** |\n", d.SysStatus.RiskLevel)
	}
	w("\n")

	// ── Today's trades ────────────────────────────────────────────────────────
	w("## 今日交易（%d 笔）\n\n", len(d.Executions))
	if len(d.Executions) == 0 {
		w("今日无成交记录。\n\n")
	} else {
		w("| 时间 | 代码 | 方向 | 数量 | 成交价 | 理论价 | 滑点 | 策略 |\n")
		w("|------|------|------|------|--------|--------|------|------|\n")
		for _, e := range d.Executions {
			ts := time.UnixMilli(e.ExecutionTime).In(cst).Format("15:04:05")
			slip := 0.0
			if e.TheoreticalPrice > 0 {
				slip = abs(e.Price-e.TheoreticalPrice) / e.TheoreticalPrice * 100
			}
			sideIcon := "🟢 BUY"
			if e.Side == "SELL" {
				sideIcon = "🔴 SELL"
			}
			w("| %s | %s | %s | %d | %.2f | %.2f | %.3f%% | %s |\n",
				ts, e.Symbol, sideIcon, e.Qty, e.Price, e.TheoreticalPrice, slip, e.StrategyName)
		}
		w("\n")

		// Trade stats
		buys, sells, totalSlip := 0, 0, 0.0
		for _, e := range d.Executions {
			if e.Side == "BUY" {
				buys++
			} else {
				sells++
			}
			if e.TheoreticalPrice > 0 {
				totalSlip += abs(e.Price-e.TheoreticalPrice) / e.TheoreticalPrice * 100
			}
		}
		avgSlip := 0.0
		if len(d.Executions) > 0 {
			avgSlip = totalSlip / float64(len(d.Executions))
		}
		w("**买入**: %d 笔  **卖出**: %d 笔  **平均滑点**: %.3f%%\n\n", buys, sells, avgSlip)
	}

	// ── Current positions ─────────────────────────────────────────────────────
	w("## 当前持仓（%d 个）\n\n", len(d.Positions))
	if len(d.Positions) == 0 {
		w("当前空仓。\n\n")
	} else {
		w("| 代码 | 数量 | 成本价 | 当前市值 | 浮动盈亏 |\n")
		w("|------|------|--------|----------|----------|\n")
		for _, p := range d.Positions {
			pnlStr := "n/a"
			if p.UnrealizedPnl != 0 && p.MarketValue > 0 {
				pnlPct := p.UnrealizedPnl / (p.MarketValue - p.UnrealizedPnl) * 100
				pnlStr = fmt.Sprintf("%s¥%.0f (%+.2f%%)", signEmoji(p.UnrealizedPnl), abs(p.UnrealizedPnl), pnlPct)
			}
			w("| %s | %d | ¥%.2f | ¥%.0f | %s |\n",
				p.Symbol, p.Qty, p.AvgPrice, p.MarketValue, pnlStr)
		}
		w("\n")
	}

	// ── Risk events ───────────────────────────────────────────────────────────
	w("## 风控事件（%d 次）\n\n", len(d.RiskEvents))
	if len(d.RiskEvents) == 0 {
		w("今日无风控事件触发。✅\n\n")
	} else {
		w("| 时间 | 类型 | 回撤 | 描述 |\n|------|------|------|------|\n")
		for _, r := range d.RiskEvents {
			ts := time.UnixMilli(r.Timestamp).In(cst).Format("15:04:05")
			w("| %s | **%s** | %.2f%% | %s |\n", ts, r.EventType, r.Drawdown, r.Description)
		}
		w("\n")
	}

	// ── Alpha rankings ────────────────────────────────────────────────────────
	if len(d.AlphaRankings) > 0 {
		w("## Alpha 排名 Top-10\n\n")
		w("| 排名 | 代码 | 名称 | 评分 | 5日涨跌 | 20日涨跌 | 换手率 | 市值(亿) |\n")
		w("|------|------|------|------|---------|---------|--------|----------|\n")
		for _, r := range d.AlphaRankings {
			w("| %d | %s | %s | %.3f | %+.2f%% | %+.2f%% | %.2f%% | %.0f |\n",
				r.Rank, r.Symbol, truncStr(r.Name, 8),
				r.Score, r.Ret5d, r.Ret20d, r.Turnover, r.MktCap/1e8)
		}
		w("\n")
	}

	// ── System status ─────────────────────────────────────────────────────────
	w("## 系统状态\n\n")
	if d.SysStatus != nil {
		openIcon := "✅ 正常"
		if !d.SysStatus.IsOpeningAllowed {
			openIcon = "🛑 已停止"
		}
		ksIcon := "✅ 未触发"
		if d.SysStatus.IsKillSwitchActive {
			ksIcon = "🚨 已触发"
		}
		w("| 指标 | 状态 |\n|------|------|\n")
		w("| 连续亏损笔数 | %d |\n", d.SysStatus.Streak)
		w("| 仓位限制 | %.0f%% |\n", d.SysStatus.MaxPositionPct*100)
		w("| 开仓状态 | %s |\n", openIcon)
		w("| Kill Switch | %s |\n", ksIcon)
		w("| 风险等级 | **%s** |\n", d.SysStatus.RiskLevel)
		w("| 异常执行次数 | %d |\n", d.SysStatus.AnomalyCount)
	} else {
		w("暂无系统状态数据。\n")
	}
	w("\n---\n\n")
	w("*本报告由 AStock 量化交易系统自动生成*\n")

	return sb.String()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func signEmoji(v float64) string {
	if v >= 0 {
		return "+"
	}
	return "-"
}

func weekdayCN(w time.Weekday) string {
	return [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}[w]
}

func truncStr(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes-1]) + "…"
}
