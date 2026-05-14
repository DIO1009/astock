// cmd/backtest is the long-period backtesting entry point.
//
// 验证目标：
//
//	【Feature 1】执行延迟 50-500ms → ExecutionRecord.Latency 显示模拟延迟
//	【Feature 2】订单簿模型 → 成交率下降至 70-90%（高波动期更低）
//	【Feature 3】流动性冲击 → 大单额外滑点 ∝ sqrt(订单量/市场成交量)
//	【Feature 4】长周期数据 → 120 交易日 × 5 intraday bars = 600 ticks
//	【Feature 5】增强统计 → 最大连续亏损 / Top5 盈利集中度 / Sharpe代理
//
// 数据说明：
//
//	使用合成历史数据（固定种子 seed=2024，可复现）。
//	真实场景请替换 generateBacktestCSV 为从行情接口拉取的 CSV。
//	每个 "tick" 代表一根 30min bar，120日×5bar = 600 ticks/symbol。
//
// 预期结果（合成数据，固定 seed）：
//
//	交易笔数 ≥ 100（真实 500+ 需日内分钟级数据或更大股票池）
//	成交率 70-90%（realistic executor v2 拒单模型）
//	滑点分布：买入 +0.4-0.7%，卖出 -0.4-0.7%
//	最大连续亏损 ≥ 3 笔（验证风控非完美过滤噪声）
package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"astock_trade/adaptive"
	"astock_trade/alpha/breakout"
	"astock_trade/alpha/momentum"
	"astock_trade/alpha/registry"
	"astock_trade/alpha/reversal"
	"astock_trade/alpha/volatility"
	"astock_trade/alpha/volume"
	"astock_trade/analysis/deviation"
	"astock_trade/broker/paper"
	"astock_trade/core"
	"astock_trade/decision/topn"
	"astock_trade/engine"
	"astock_trade/execctrl"
	"astock_trade/executor/realistic"
	"astock_trade/logger/console"
	"astock_trade/logger/execution"
	"astock_trade/market/trend"
	"astock_trade/monitor"
	"astock_trade/performance"
	"astock_trade/portfolio"
	"astock_trade/position"
	"astock_trade/provider/replay"
	"astock_trade/review/weekly"
	"astock_trade/screener/static"
	"astock_trade/signal/dampener"
	"astock_trade/signal/stability"
)

const (
	initialCapital = 200_000.0
	tickInterval   = 10 * time.Millisecond // 回测：尽快推进
	tradingDays    = 120                   // 6 个月
	barsPerDay     = 5                     // 每日 5 根 30min bar (9:30-14:30)
	backtestSeed   = int64(2024)
	csvDataPath    = "backtest_data.csv"
	execLogPath    = "backtest_executions.jsonl"
	tradeLogPath          = "backtest_trades.jsonl"
	indexSymbol           = "000300"
	tradingCostConfigPath = "config/trading_cost.json"
)

// 回测股票池：10 只代表性 A 股（大中小盘混合）
var symbols = []string{
	"600519", // 贵州茅台
	"000858", // 五粮液
	"300750", // 宁德时代
	"601318", // 中国平安
	"600036", // 招商银行
	"000001", // 平安银行
	"601166", // 兴业银行
	"600276", // 恒瑞医药
	"600900", // 长江电力
	"002594", // 比亚迪
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	printBanner()

	// ── Feature 4: 生成长周期历史数据 ─────────────────────────────────────────
	totalBars := tradingDays*barsPerDay + 25 // +25 for EMA warmup
	allSymbols := append(append([]string{}, symbols...), indexSymbol)
	generateBacktestCSV(csvDataPath, allSymbols, totalBars, backtestSeed)

	dataProvider := replay.New()
	if err := dataProvider.LoadCSV(csvDataPath); err != nil {
		log.Fatalf("[Backtest] 数据加载失败: %v", err)
	}
	for _, sym := range allSymbols {
		log.Printf("[Backtest] 已加载 %-8s %d bars  (含预热 %d)",
			sym, dataProvider.BarCount(sym), 25)
	}

	// ── Feature 3 日志 ────────────────────────────────────────────────────────
	execLogger, err := execution.New(execLogPath)
	if err != nil {
		log.Fatalf("[Backtest] 执行日志失败: %v", err)
	}
	defer execLogger.Close()

	// ── Feature 1+2+3: realistic executor v2 ─────────────────────────────────
	cfg := realistic.Default() // 已含延迟/订单簿/冲击参数
	loadedCfg, err := realistic.LoadTradingCostConfig(tradingCostConfigPath, cfg)
	if err != nil {
		log.Fatalf("[Backtest] 加载交易费率配置失败: %v", err)
	}
	innerExec := realistic.New(loadedCfg)
	paperBroker := paper.New(innerExec, initialCapital)
	paperBroker.SetLogger(execLogger.Log)
	log.Printf("[Backtest] 执行器: %s", innerExec.CostSummary())

	// ── Feature 4: 监控（回测模式静默，仅记录，final report 展示）────────────
	mon := monitor.New(monitor.Config{
		CautionDrawdownPct:   5.0,
		DefenseDrawdownPct:   10.0,
		EmergencyDrawdownPct: 15.0,
		ReportEveryNTicks:    50, // 回测不需要高频打印
	})
	mon.OnAlert(func(evt core.AlertEvent) {
		if evt.Level >= core.RiskDefense {
			log.Printf("⚠️  [Backtest] %s", evt.Message)
		}
	})

	// ── 策略层（与 main.go v11 完全相同配置）────────────────────────────────
	alphaReg := registry.New(
		registry.Config{UpdateEvery: 10, Lambda: 0.40, MinFactor: 0.20, MaxFactor: 3.0},
		registry.Entry{
			Alpha: momentum.New(momentum.Config{
				MaxReturn5d: 10.0, MaxReturn20d: 20.0, Weight5d: 0.4,
			}),
			BaseWeight: 0.30,
		},
		registry.Entry{
			Alpha: reversal.New(reversal.Config{
				ThresholdPct: 0.03, MaxReturn5d: 10.0, WeightMA: 0.6,
			}),
			BaseWeight: 0.25,
		},
		registry.Entry{
			Alpha:      breakout.New(breakout.Config{BreakoutThreshold: 8.0, RefVolume: 500_000}),
			BaseWeight: 0.20,
		},
		registry.Entry{
			Alpha:      volume.New(volume.Config{RefVolume: 500_000}),
			BaseWeight: 0.15,
		},
		registry.Entry{
			Alpha:      volatility.New(volatility.Config{MaxVol: 3.0}),
			BaseWeight: 0.10,
		},
	)

	antimono := dampener.New(dampener.Config{MaxTop1Streak: 3, DampenFactor: 0.6})
	stab := stability.New(stability.Config{TopN: 3, MinConsecutive: 2})
	marketFilter := trend.New(trend.Config{
		Period: 8, UptrendThreshold: 0.005, DowntrendThreshold: 0.005,
	})
	portDecision := topn.New(topn.Config{
		MaxPositions: 5, // 10 只股票，最多同时持 5 只
		TopN:         5,
		BuyThreshold: 0.08,
	})
	posMgr := position.New(position.Config{
		StopLossPct:   0.05,
		TakeProfitPct: 0.25,
		TrailStart:    0.06,
		TrailDrop:     0.02,
	})
	portMgr := portfolio.New(portfolio.Config{
		TotalCapital: initialCapital,
		MaxPositions: 5,
		MaxSinglePct: 0.25,
		MaxTotalPct:  0.85,
		RankPcts:     []float64{0.25, 0.25, 0.20, 0.15, 0.15},
	})
	execCtrl := execctrl.New(execctrl.Config{
		CooldownTicksLoss:   3,
		CooldownTicksProfit: 2,
		HighPriceBlockTicks: 10,
		MinHoldTicks:        2,
		MaxBuyPerTick:       3,
		MaxSellPerTick:      3,
	})
	perfTracker := performance.New(performance.Config{
		InitialCapital:    initialCapital,
		ReportEveryNTicks: 100, // 每 100 tick 打印一次
	})

	tradeLogger := console.New()
	reviewer := weekly.New(tradeLogPath)
	screener := static.New(symbols)

	eng := engine.New(
		engine.Config{
			TickInterval:      tickInterval,
			ReviewWeekday:     time.Friday,
			ReviewHour:        18,
			LogRank:           false, // 回测关闭逐 tick 详细日志
			IndexSymbol:       indexSymbol,
			OscillateMinScore: 0.30,
		},
		screener,
		dataProvider,
		alphaReg,
		antimono,
		stab,
		marketFilter,
		portDecision,
		posMgr,
		portMgr,
		execCtrl,
		perfTracker,
		paperBroker,
		tradeLogger,
		reviewer,
	)

	eng.SetAdaptiveOptimizer(adaptive.New(adaptive.Config{
		DrawdownThreshold:  10.0,
		WinRateThreshold:   30.0,
		MinTrades:          10,
		NormalMaxTotalPct:  0.85,
		ReducedMaxTotalPct: 0.50,
		NormalBuyThreshold: 0.08,
		RaisedBuyThreshold: 0.15,
	}))
	eng.SetMonitor(mon)

	// ── 计算超时时间：总 tick 数 × tickInterval × 安全系数 ────────────────────
	deliverableBars := dataProvider.BarCount(symbols[0]) - 25 // 减去预热
	if deliverableBars < 1 {
		deliverableBars = 1
	}
	runTimeout := time.Duration(deliverableBars)*tickInterval*3 + 5*time.Second
	log.Printf("[Backtest] 预计 tick 数: %d  超时保护: %v", deliverableBars, runTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	log.Println("[Backtest] 开始回测...")
	start := time.Now()
	if err := eng.Run(runCtx); err != nil && err != context.DeadlineExceeded {
		log.Fatalf("[Backtest] 引擎异常: %v", err)
	}
	elapsed := time.Since(start)
	log.Printf("[Backtest] 回测完成，耗时 %v", elapsed)

	// ── 最终报告 ──────────────────────────────────────────────────────────────
	mon.PrintFinalReport()
	printEnhancedReport(perfTracker.Report())

	// ── Feature 5: 实盘偏差分析 ───────────────────────────────────────────────
	records := paperBroker.Records()
	if len(records) > 0 {
		analyzer := deviation.New()
		analyzer.AddAll(records)
		analyzer.PrintReport()
	}

	total, filled, partial, rejected := paperBroker.Stats()
	fillRate := 0.0
	if total > 0 {
		fillRate = float64(filled+partial) / float64(total) * 100
	}
	log.Printf("[Backtest] Broker: 总=%d  全成=%d  部分=%d  拒绝=%d  成交率=%.1f%%",
		total, filled, partial, rejected, fillRate)

	if fillRate < 70 {
		log.Printf("⚠️  [Backtest] 成交率 %.1f%% 低于 70%% 目标，考虑减小仓位或降低执行延迟上限", fillRate)
	} else if fillRate > 90 {
		log.Printf("⚡ [Backtest] 成交率 %.1f%% 高于 90%%，拒单模型可进一步收紧", fillRate)
	} else {
		log.Printf("✅ [Backtest] 成交率 %.1f%% 在目标范围 70-90%%", fillRate)
	}
}

// printEnhancedReport 打印完整的增强统计报告（Feature 5）。
func printEnhancedReport(r core.PerformanceReport) {
	line := "══════════════════════════════════════════════════════════════"
	sep := "──────────────────────────────────────────────────────────────"
	log.Println(line)
	log.Println("  长周期回测增强报告  (Enhanced Backtest Report)")
	log.Println(line)
	log.Printf("  总收益率      %+.2f%%  │  最大回撤      %.2f%%", r.TotalReturn, r.MaxDrawdown)
	log.Printf("  胜率          %.1f%%    │  盈亏比        %.2f", r.WinRate, r.ProfitFactor)
	log.Printf("  平均盈利      %+.2f%%  │  平均亏损      %.2f%%", r.AvgWin, r.AvgLoss)
	log.Printf("  总交易次数    %d      │  平均持仓      %.1f tick", r.TradeCount, r.AvgHoldTicks)
	log.Printf("  出场原因      SL=%d  TP=%d  TRAIL=%d", r.StopLossCount, r.TakeProfitCount, r.TrailStopCount)
	log.Println(sep)
	log.Println("  Feature 5 — 增强统计")
	log.Println(sep)
	log.Printf("  最大连续亏损  %d笔 / 累计 %.2f%%", r.MaxConsecutiveLoss, r.MaxConsecutiveLossPct)
	log.Printf("  Top5盈利集中度 %.1f%%  (>60%%表示依赖少数大赢)", r.Top5PnlConcentration)
	log.Printf("  Sharpe代理    %.2f   (>1.0 可接受, >2.0 优秀)", r.SharpeProxy)
	log.Printf("  期初资金      ¥%.0f  │  最终权益      ¥%.2f", r.InitialCapital, r.CurrentEquity)
	log.Println(line)
}

// generateBacktestCSV 生成回测用合成 OHLCV 数据。
// 使用固定种子确保可复现；真实场景替换为行情 API 下载。
//
// 价格模型：
//   - 日间随机游走，drift=0（中性市场）
//   - 每根 bar 内 O/H/L/C 符合 OHLC 约束
//   - 成交量随机 [500K, 8M]（模拟不同流动性）
func generateBacktestCSV(path string, symbols []string, nBars int, seed int64) {
	rng := rand.New(rand.NewSource(seed))

	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("[Backtest] 无法创建数据文件: %v", err)
	}
	defer f.Close()

	fmt.Fprintln(f, "date,symbol,open,high,low,close,volume")

	// 各股票种子价格（接近真实数量级）
	seedPrices := map[string]float64{
		"600519": 1750.0, "000858": 115.0, "300750": 165.0,
		"601318": 45.0, "600036": 38.0, "000001": 12.0,
		"601166": 22.0, "600276": 50.0, "600900": 25.0,
		"002594": 280.0, "000300": 3400.0,
	}
	// 各股票日波动率（大盘股低，小盘股高）
	dailyVol := map[string]float64{
		"600519": 0.012, "000858": 0.016, "300750": 0.022,
		"601318": 0.015, "600036": 0.014, "000001": 0.018,
		"601166": 0.017, "600276": 0.020, "600900": 0.011,
		"002594": 0.025, "000300": 0.010,
	}

	prices := make(map[string]float64, len(symbols))
	for _, sym := range symbols {
		if p, ok := seedPrices[sym]; ok {
			prices[sym] = p
		} else {
			prices[sym] = 50.0 + rng.Float64()*150.0
		}
	}

	// 生成时间序列（跳过周末）
	startDate := time.Date(2023, 7, 3, 0, 0, 0, 0, time.Local)
	barIdx := 0

	for barIdx < nBars {
		if startDate.Weekday() == time.Saturday || startDate.Weekday() == time.Sunday {
			startDate = startDate.AddDate(0, 0, 1)
			continue
		}
		dateStr := startDate.Format("2006-01-02")

		for _, sym := range symbols {
			prev := prices[sym]
			vol := dailyVol[sym]
			if v, ok := dailyVol[sym]; ok {
				vol = v
			}

			// 单根 bar 的 OHLCV
			drift := rng.NormFloat64() * vol * 0.6 // intraday drift (中性)
			closeP := math.Max(0.5, prev*(1+drift))
			openP := math.Max(0.5, prev*(1+rng.NormFloat64()*vol*0.2))

			high := math.Max(openP, closeP) * (1 + math.Abs(rng.NormFloat64())*vol*0.4)
			low := math.Min(openP, closeP) * (1 - math.Abs(rng.NormFloat64())*vol*0.4)
			// 保证 OHLC 约束
			high = math.Max(high, math.Max(openP, closeP))
			low = math.Min(low, math.Min(openP, closeP))

			// 成交量：蓝筹股量大，成长股量小
			baseVol := int64(1_000_000 + rng.Intn(7_000_000))
			if sym == "600519" { // 茅台成交量相对较小（高单价）
				baseVol = int64(200_000 + rng.Intn(800_000))
			}

			fmt.Fprintf(f, "%s,%s,%.4f,%.4f,%.4f,%.4f,%d\n",
				dateStr, sym, openP, high, low, closeP, baseVol)

			prices[sym] = closeP
		}

		startDate = startDate.AddDate(0, 0, 1)
		barIdx++
	}

	log.Printf("[Backtest] 生成 %s: %d bars × %d 标的 (seed=%d)",
		path, nBars, len(symbols), seed)
}

func printBanner() {
	log.Println("════════════════════════════════════════════════════════════════")
	log.Println("  A股量化交易系统 — 长周期回测模式 (Backtest)")
	log.Println("  执行延迟 50-500ms | 订单簿模型 | 流动性冲击 | 增强统计")
	log.Println("════════════════════════════════════════════════════════════════")
}
