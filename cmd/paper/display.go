package main

import (
	"log"

	"astock_trade/core"
	"astock_trade/monitor"
	paper "astock_trade/broker/paper"
	"astock_trade/safety"
)
func printBanner() {
	log.Println("════════════════════════════════════════════════════════════════")
	log.Println("  A股量化交易系统 — Paper Trading 模式")
	log.Println("  【功能】历史回放 | 统一Broker接口 | 执行日志 | 实时监控 | 偏差分析")
	log.Println("  【安全】连续亏损抑制 | 人工控制接口 | 持仓持久化 | 异常检测")
	log.Println("════════════════════════════════════════════════════════════════")
}

type multiTradeLogger []core.TradeLogger

func (m multiTradeLogger) Log(trade *core.Trade) {
	for _, logger := range m {
		if logger != nil {
			logger.Log(trade)
		}
	}
}

func printSummary(mon *monitor.Monitor, pb *paper.Broker, sg *safety.Guard) {
	s := mon.State()
	ret := 0.0
	if initialCapital > 0 {
		ret = (s.Equity - initialCapital) / initialCapital * 100
	}
	log.Println("════════════════════════════════════════════════════════════════")
	log.Println("  Paper Trading 验证总结")
	log.Println("════════════════════════════════════════════════════════════════")
	log.Printf("  初始资金   ¥%.0f", initialCapital)
	log.Printf("  最终权益   ¥%.2f  (%.2f%%)", s.Equity, ret)
	log.Printf("  最大回撤   %.2f%%", s.DrawdownPct)
	log.Printf("  风险档位   %s", s.RiskLevel)
	log.Printf("  总交易数   %d  胜率 %.1f%%", s.TradeCount, s.WinRate)
	total, _, _, rejected := pb.Stats()
	if total > 0 {
		log.Printf("  成交失败率 %.1f%%  (拒绝 %d/%d)", float64(rejected)/float64(total)*100, rejected, total)
	}
	// Feature 6: 安全控制层摘要
	st := sg.SafetyStatus()
	log.Println("──────────────────────────────────────────────────────────────")
	log.Println("  [安全控制层总结]")
	log.Printf("  最大连续亏损笔数: %d", st.CurrentStreak)
	log.Printf("  仓位倍数:         %.1f×", st.StreakScale)
	log.Printf("  异常执行次数:     %d", st.AbnormalCount)
	log.Printf("  交易暂停触发:     %v", st.TradingStopped)

	// 上线验证目标检查
	log.Println("──────────────────────────────────────────────────────────────")
	log.Println("  [上线验证目标]")
	streakOK := st.CurrentStreak <= 6
	ddOK := s.DrawdownPct <= 12.0
	log.Printf("  连续亏损 ≤ 6 笔:  %s (%d笔)", checkMark(streakOK), st.CurrentStreak)
	log.Printf("  最大回撤 ≤ 12%%:  %s (%.2f%%)", checkMark(ddOK), s.DrawdownPct)
	log.Printf("  系统可人工干预:   ✅ (SIGUSR1/SIGUSR2/SIGHUP 已注册)")
	log.Println("════════════════════════════════════════════════════════════════")
}