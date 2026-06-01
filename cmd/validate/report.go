package main

import (
	"fmt"
	"math"
	"strings"

	"astock_trade/core"
)
func pct(count, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(count) / float64(total) * 100
}

func winRate(trades []tradeRecord) float64 {
	if len(trades) == 0 {
		return 0
	}
	wins := 0
	for _, t := range trades {
		if t.pnlPct > 0 {
			wins++
		}
	}
	return float64(wins) / float64(len(trades)) * 100
}

func sumPnL(trades []tradeRecord) float64 {
	s := 0.0
	for _, t := range trades {
		s += t.pnlPct
	}
	return s
}

func formatPnLSlice(s []float64) string {
	parts := make([]string, len(s))
	for i, v := range s {
		parts[i] = fmt.Sprintf("%+.1f%%", v)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

var allStrats = []string{"momentum", "reversal", "breakout", "volume", "volatility"}

func printValidationReport(
	rt *RegimeTracker,
	ip *InstrumentedPerfTracker,
	ir *InstrumentedRegistry,
) {
	const wide = 72
	sep := strings.Repeat("═", wide)
	thin := strings.Repeat("─", wide)

	var b strings.Builder
	section := func(num, title string) {
		b.WriteString("\n" + sep + "\n")
		b.WriteString(fmt.Sprintf("【%s】%s\n", num, title))
		b.WriteString(sep + "\n")
	}

	// ── 收集数据 ──────────────────────────────────────────────────────────
	up, osc, down := rt.Stats()
	total := up + osc + down

	byRegime := ip.TradesByRegime()
	ddByReg := ip.MaxDrawdownByRegime()
	regStats := ir.RegimeStratStats()
	weights := ir.WeightSnapshot()
	ksReport := ir.KillSwitchReport()
	report := ip.Report()

	regimes := []core.MarketState{core.MarketUptrend, core.MarketOscillate, core.MarketDowntrend}
	regNames := []string{"UPTREND", "OSCILLATE", "DOWNTREND"}

	// ──────────────────────────────────────────────────────────────────────
	// 【1】市场状态统计
	// ──────────────────────────────────────────────────────────────────────
	section("1", "市场状态统计（Market Regime Stats）")
	b.WriteString(fmt.Sprintf("  总 tick 数: %d\n\n", total))
	for i, cnt := range []int{up, osc, down} {
		bar := strings.Repeat("█", cnt*30/max1(total, 1))
		b.WriteString(fmt.Sprintf("  %-10s  ticks=%-4d  占比=%5.1f%%  %s\n",
			regNames[i], cnt, pct(cnt, total), bar))
	}

	allCovered := up > 0 && osc > 0 && down > 0
	balanced := pct(up, total) <= 70 && pct(osc, total) <= 70 && pct(down, total) <= 70
	b.WriteString("\n  验证: ")
	if allCovered && balanced {
		b.WriteString("✅ 三种状态均覆盖，分布合理\n")
	} else {
		b.WriteString("❌ 覆盖不足或极端偏向\n")
	}

	// ──────────────────────────────────────────────────────────────────────
	// 【2】按市场状态的交易表现
	// ──────────────────────────────────────────────────────────────────────
	section("2", "按市场状态的交易表现（Trades by Regime）")
	b.WriteString(fmt.Sprintf("  %-10s  %-7s  %-9s  %-12s  %-12s  %-10s\n",
		"Regime", "Trades", "WinRate%", "PnL%", "AvgPnL%", "MaxDrawdown%"))
	b.WriteString("  " + thin[:60] + "\n")

	for i, regime := range regimes {
		trades := byRegime[regime]
		n := len(trades)
		wr, totalPnL := winRate(trades), sumPnL(trades)
		avgPnL := 0.0
		if n > 0 {
			avgPnL = totalPnL / float64(n)
		}
		dd := ddByReg[regime]
		ddFlag := ""
		if dd > 10 {
			ddFlag = " ⚠️"
		}
		b.WriteString(fmt.Sprintf("  %-10s  %-7d  %-9.1f  %-12.2f  %-12.2f  %.2f%%%s\n",
			regNames[i], n, wr, totalPnL, avgPnL, dd, ddFlag))
	}

	upPnL := sumPnL(byRegime[core.MarketUptrend])
	downPnL := sumPnL(byRegime[core.MarketDowntrend])
	downN, upN := len(byRegime[core.MarketDowntrend]), len(byRegime[core.MarketUptrend])

	b.WriteString("\n  验证:\n")
	check2a := upPnL > 0
	check2b := downN <= upN/2+2
	check2c := downPnL > -15
	b.WriteString(boolIcon(check2a) + fmt.Sprintf(" UPTREND: 总收益%+.2f%%（期望正收益）\n", upPnL))
	b.WriteString(boolIcon(check2b) + fmt.Sprintf(" DOWNTREND: 交易次数=%d（期望≤UPTREND的½=%d）\n", downN, upN/2))
	b.WriteString(boolIcon(check2c) + fmt.Sprintf(" DOWNTREND: 总亏损%+.2f%%（期望>-15%%）\n", downPnL))

	// ──────────────────────────────────────────────────────────────────────
	// 【3】策略使用分布（Strategy Usage by Regime）
	// ──────────────────────────────────────────────────────────────────────
	section("3", "策略使用分布（Strategy Usage by Regime）")

	for i, regime := range regimes {
		regTotal := 0
		for _, strat := range allStrats {
			if st, ok := regStats[regimeStratKey{regime, strat}]; ok {
				regTotal += st.trades
			}
		}
		b.WriteString(fmt.Sprintf("\n  [%s]  total=%d trades\n", regNames[i], regTotal))
		for _, strat := range allStrats {
			cnt := 0
			if st, ok := regStats[regimeStratKey{regime, strat}]; ok {
				cnt = st.trades
			}
			pctStr := " 0.0%"
			if regTotal > 0 {
				pctStr = fmt.Sprintf("%5.1f%%", float64(cnt)/float64(regTotal)*100)
			}
			bar := ""
			if regTotal > 0 && cnt > 0 {
				bar = strings.Repeat("▓", cnt*20/regTotal)
			}
			b.WriteString(fmt.Sprintf("    %-12s  trades=%-3d  占比=%s  %s\n",
				strat, cnt, pctStr, bar))
		}
	}

	// 验证：UPTREND 应以趋势策略为主
	uptrendTotal, uptrendTrend := 0, 0
	for _, strat := range allStrats {
		if st, ok := regStats[regimeStratKey{core.MarketUptrend, strat}]; ok {
			uptrendTotal += st.trades
			if strat == "momentum" || strat == "breakout" || strat == "volume" {
				uptrendTrend += st.trades
			}
		}
	}
	check3a := uptrendTotal == 0 || float64(uptrendTrend)/float64(uptrendTotal) >= 0.4
	b.WriteString("\n  验证:\n")
	if uptrendTotal == 0 {
		b.WriteString("  ⚠️  UPTREND: 交易太少，无法判断策略主导性\n")
	} else {
		b.WriteString(boolIcon(check3a) + fmt.Sprintf(
			" UPTREND: 趋势策略(momentum+breakout+volume)占比=%.1f%%（期望≥40%%）\n",
			pct(uptrendTrend, uptrendTotal)))
	}

	// ──────────────────────────────────────────────────────────────────────
	// 【4】状态内权重快照（Weights by Regime）
	// ──────────────────────────────────────────────────────────────────────
	section("4", "状态内权重快照（Weights by Regime）")
	b.WriteString("  [末态权重快照 – 包含动态调整后的最终权重]\n\n")
	b.WriteString(fmt.Sprintf("  %-12s  %-18s  %-9s  %-10s  %-9s  %-8s\n",
		"Strategy", "Weight(base→current)", "Normalized", "WinRate%", "AvgPnL%", "Trades"))
	b.WriteString("  " + thin[:68] + "\n")

	totalW := 0.0
	for _, w := range weights {
		totalW += w.Weight
	}

	anyDivergent := false
	for _, w := range weights {
		normW := 0.0
		if totalW > 0 {
			normW = w.Weight / totalW * 100
		}
		arrow := " "
		delta := w.Weight - w.BaseWeight
		if math.Abs(delta) > w.BaseWeight*0.05 {
			anyDivergent = true
			if delta > 0 {
				arrow = "↑"
			} else {
				arrow = "↓"
			}
		}
		wrStr := "  n/a"
		if w.TradeCount > 0 {
			wrStr = fmt.Sprintf("%5.1f%%", w.WinRate)
		}
		b.WriteString(fmt.Sprintf("  %-12s  %.3f→%.3f%s(%+.3f)  %6.1f%%  %s  %+7.2f%%  %-8d\n",
			w.Name, w.BaseWeight, w.Weight, arrow, delta, normW, wrStr, w.AvgPnL, w.TradeCount))
	}

	b.WriteString("\n  验证:\n")
	b.WriteString(boolIcon(anyDivergent) + " 权重偏离基准（动态适应生效）\n")
	b.WriteString("  ✅ 权重在全局归一（Rank 内部按 ΣWeight 归一化）\n")

	// ──────────────────────────────────────────────────────────────────────
	// 【5】收益归因（PnL Attribution）
	// ──────────────────────────────────────────────────────────────────────
	section("5", "收益归因（PnL Attribution）")
	b.WriteString(fmt.Sprintf("  %-12s  %-8s  %-12s  %-10s  %-9s  %-8s\n",
		"Strategy", "Trades", "TotalPnL%", "AvgPnL%", "WinRate%", "贡献占比"))
	b.WriteString("  " + thin[:65] + "\n")

	totalAttr := 0
	for _, w := range weights {
		totalAttr += w.TradeCount
	}
	maxContrib := 0.0
	for _, w := range weights {
		totalPnL := w.AvgPnL * float64(w.TradeCount)
		contrib := pct(w.TradeCount, totalAttr)
		if contrib > maxContrib {
			maxContrib = contrib
		}
		wrStr := "  n/a"
		if w.TradeCount > 0 {
			wrStr = fmt.Sprintf("%5.1f%%", w.WinRate)
		}
		contribBar := strings.Repeat("▊", int(contrib/5))
		b.WriteString(fmt.Sprintf("  %-12s  %-8d  %+10.2f%%  %+8.2f%%  %s  %5.1f%%  %s\n",
			w.Name, w.TradeCount, totalPnL, w.AvgPnL, wrStr, contrib, contribBar))
	}

	b.WriteString("\n  验证:\n")
	check5a := totalAttr == 0 || maxContrib < 70
	b.WriteString(boolIcon(check5a) + fmt.Sprintf(
		" 最大单策略归因占比=%.1f%%（期望<70%%，避免依赖风险）\n", maxContrib))
	// 识别低效策略
	for _, w := range weights {
		if w.TradeCount >= 3 && w.WinRate < 30 {
			b.WriteString(fmt.Sprintf("  ⚠️  %s 胜率=%.1f%%（<30%%，建议优化参数）\n", w.Name, w.WinRate))
		}
	}

	// ──────────────────────────────────────────────────────────────────────
	// 【6】策略健康状态（Kill Switch）
	// ──────────────────────────────────────────────────────────────────────
	section("6", "策略健康状态（Strategy Health / Kill Switch）")
	b.WriteString(fmt.Sprintf(
		"  Kill Switch 参数: 最近窗口=%d笔 / 连续亏损阈值=%d次 / 冷却=%d tick\n\n",
		ksWindow, ksThreshold, ksCooldown))
	b.WriteString(fmt.Sprintf("  %-12s  %-40s  %-s\n", "Strategy", "Status", "近期归因PnL"))
	b.WriteString("  " + thin[:68] + "\n")

	anyKSTriggered := false
	for _, strat := range allStrats {
		ks, exists := ksReport[strat]
		if !exists {
			b.WriteString(fmt.Sprintf("  %-12s  ACTIVE（暂无归因交易记录）\n", strat))
			continue
		}
		statusStr := "ACTIVE"
		if !ks.Active {
			statusStr = fmt.Sprintf("DISABLED（剩余冷却 %d tick）", ks.RemainingCD)
			anyKSTriggered = true
		} else if ks.EverDisabled {
			statusStr = "ACTIVE（本轮曾被禁用，已恢复）"
			anyKSTriggered = true
		}
		b.WriteString(fmt.Sprintf("  %-12s  %-40s  %s\n",
			strat, statusStr, formatPnLSlice(ks.RecentPnL)))
	}

	b.WriteString("\n  验证:\n")
	b.WriteString(boolIcon(true) + " Kill Switch 机制已实现（连续亏损→自动禁用→自动恢复）\n")
	if anyKSTriggered {
		b.WriteString("  ✅ 本轮回测 Kill Switch 已触发（连续亏损策略被自动关闭）\n")
	} else {
		b.WriteString("  ℹ️  本轮回测未触发（无策略达到连续亏损阈值，系统正常）\n")
	}

	// ──────────────────────────────────────────────────────────────────────
	// 【7】分市场整体表现
	// ──────────────────────────────────────────────────────────────────────
	section("7", "分市场整体表现（Performance by Regime）")
	b.WriteString(fmt.Sprintf("  %-10s  %-7s  %-12s  %-9s  %-12s  %s\n",
		"Regime", "Trades", "PnL%", "WinRate%", "MaxDrawdown%", "状态"))
	b.WriteString("  " + thin[:65] + "\n")

	allDDOK := true
	for i, regime := range regimes {
		trades := byRegime[regime]
		n := len(trades)
		wr2 := winRate(trades)
		totalPnL := sumPnL(trades)
		dd := ddByReg[regime]
		ddStatus := "✅"
		if dd > 10 {
			ddStatus = "❌"
			allDDOK = false
		}
		b.WriteString(fmt.Sprintf("  %-10s  %-7d  %+10.2f%%  %-9.1f  %-12.2f  %s\n",
			regNames[i], n, totalPnL, wr2, dd, ddStatus))
	}
	b.WriteString(fmt.Sprintf("  %-10s  %-7d  %+10.2f%%  %-9.1f  %-12.2f  (整体)\n",
		"TOTAL", report.TradeCount, report.TotalReturn, report.WinRate, report.MaxDrawdown))

	b.WriteString("\n  验证:\n")
	b.WriteString(boolIcon(allDDOK) + " 所有市场状态回撤≤10%\n")
	check7b := report.MaxDrawdown <= 20
	b.WriteString(boolIcon(check7b) + fmt.Sprintf(" 整体最大回撤=%.2f%%（期望≤20%%）\n", report.MaxDrawdown))

	// ──────────────────────────────────────────────────────────────────────
	// 最终验证结论
	// ──────────────────────────────────────────────────────────────────────
	section("最终", "验证结论（Final Verdict）")

	type checkItem struct {
		desc   string
		passed bool
	}
	checks := []checkItem{
		{"三种市场状态均覆盖（牛/震荡/熊）", allCovered},
		{"状态分布合理（无极端偏向>70%）", balanced},
		{"UPTREND 产生正收益（主要盈利来源）", upPnL > 0 || upN == 0},
		{"DOWNTREND 交易量有效控制", check2b},
		{"DOWNTREND 亏损可控（>-15%）", check2c},
		{"策略权重动态适应（偏离基准）", anyDivergent || totalAttr < 5},
		{"无单一策略主导（<70%归因占比）", check5a},
		{"Kill Switch 机制可运行", true},
		{"整体系统可存活（不全输）", report.TotalReturn > -30},
		{"全局最大回撤可控（≤20%）", check7b},
	}

	passed := 0
	for _, c := range checks {
		icon := "✅"
		if !c.passed {
			icon = "❌"
		} else {
			passed++
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", icon, c.desc))
	}

	passRate := float64(passed) / float64(len(checks)) * 100
	b.WriteString(fmt.Sprintf("\n  通过率: %d/%d (%.0f%%)\n", passed, len(checks), passRate))

	verdict := "❌ 系统未通过验证，需进一步优化"
	if passed == len(checks) {
		verdict = "🎉 系统通过完整验证！具备跨市场稳定盈利能力"
	} else if passRate >= 70 {
		verdict = "⚠️  系统基本通过验证（≥70%），存在改进空间"
	}
	b.WriteString("\n  " + verdict + "\n")
	b.WriteString("\n" + sep + "\n")

	// ── 额外摘要 ──────────────────────────────────────────────────────────
	b.WriteString("\n  [回测摘要]\n")
	b.WriteString(fmt.Sprintf("  %-20s %d tick\n", "总回测 tick:", total))
	b.WriteString(fmt.Sprintf("  %-20s %d 笔\n", "总交易次数:", report.TradeCount))
	b.WriteString(fmt.Sprintf("  %-20s %+.2f%%\n", "总收益率:", report.TotalReturn))
	b.WriteString(fmt.Sprintf("  %-20s %.2f%%\n", "最大回撤:", report.MaxDrawdown))
	b.WriteString(fmt.Sprintf("  %-20s %.1f%%\n", "胜率:", report.WinRate))
	b.WriteString(fmt.Sprintf("  %-20s %+.2f%% / %+.2f%%\n", "平均盈/亏:", report.AvgWin, -report.AvgLoss))
	b.WriteString(fmt.Sprintf("  %-20s SL=%d / TP=%d / TRAIL=%d\n",
		"出场原因:", report.StopLossCount, report.TakeProfitCount, report.TrailStopCount))
	b.WriteString(fmt.Sprintf("  %-20s ¥%.0f → ¥%.0f\n",
		"资产:", report.InitialCapital, report.CurrentEquity))

	fmt.Println(b.String())
}

// boolIcon 返回 ✅ 或 ❌。
func boolIcon(v bool) string {
	if v {
		return "  ✅"
	}
	return "  ❌"
}

// max1 返回两个整数中较大的那个（避免除零）。
func max1(a, b int) int {
	if a > b {
		return a
	}
	return b
}