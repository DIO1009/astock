// main is the composition root for the A-share quantitative trading simulation.
//
// v11 升级：多策略自适应系统（Alpha Portfolio）
//
//	【策略层】5 类正交策略，通过 Registry 动态加权组合
//	  momentum  (基础权重 0.30) — 多周期趋势跟踪（5d + 20d）
//	  reversal  (基础权重 0.25) — 超跌反弹均值回归（EMA偏离反向信号）
//	  breakout  (基础权重 0.20) — 量价突破（成交量确认的短期突破）
//	  volume    (基础权重 0.15) — 相对成交量（机构参与度代理）
//	  volatility(基础权重 0.10) — 波动率惩罚（纯风险折扣因子）
//
//	【自适应层】每 20 tick 更新策略权重
//	  表现好的策略 → 权重最多增加 40%
//	  表现差的策略 → 权重最多降低 40%
//
//	【风控自适应】
//	  MaxDrawdown > 8%  → 仓位上限从 80% 降至 50%
//	  WinRate < 35%     → 入场门槛从 0.08 提高到 0.15
package main

import (
	"context"
	"log"
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
	"astock_trade/decision/topn"
	"astock_trade/engine"
	"astock_trade/execctrl"
	"astock_trade/executor/simulated"
	"astock_trade/logger/console"
	"astock_trade/market/trend"
	"astock_trade/performance"
	"astock_trade/portfolio"
	"astock_trade/position"
	"astock_trade/provider/mock"
	"astock_trade/review/weekly"
	"astock_trade/screener/static"
	"astock_trade/signal/dampener"
	"astock_trade/signal/stability"
)

const (
	demoRunDuration = 200 * time.Second // 200 tick — 足够观察策略权重轮动和自适应规则触发
	tradeLogPath    = "trades.jsonl"
	indexSymbol     = "000300"
)

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Println("════════════════════════════════════════════════════════════════")
	log.Println("  A股量化交易模拟系统 v11 – 多策略自适应 Alpha Portfolio")
	log.Println("════════════════════════════════════════════════════════════════")

	screener := static.New([]string{
		"600519", "000858", "600809", "000568", "600887", "000333", "000651", "600690",
		"300750", "002594", "601012", "002475", "002415", "000063", "688981", "300059",
		"300760", "603259", "600276", "002714", "601318", "600036", "601166", "601398",
		"601288", "600030", "601668", "600031", "600900", "601857", "600938", "601088",
		"601899", "600309", "600050", "601888",
	})
	provider := mock.New()

	// ── 五策略动态权重 Registry ───────────────────────────────────────────────
	//
	//  正交性：
	//    momentum  + reversal  → 趋势方向相反，UPTREND 时 momentum 主导
	//    breakout              → 只在量价爆发时激活
	//    volume                → 参与度辅助因子
	//    volatility            → 纯风险折扣（[-1,0] 区间）
	//
	//  动态权重参数：
	//    UpdateEvery = 20 tick（每 ~20 秒更新一次，对应约 1 周期行情）
	//    Lambda      = 0.40  （权重最多偏离基准 ±40%）
	alphaReg := registry.New(
		registry.Config{
			UpdateEvery: 20,   // 每 20 tick 重新评估一次策略权重
			Lambda:      0.40, // 适应速度：表现好的策略最多得 1.4× 基础权重
			MinFactor:   0.20, // 防止权重归零
			MaxFactor:   3.0,  // 防止权重垄断
		},
		registry.Entry{
			Alpha: momentum.New(momentum.Config{
				MaxReturn5d:  10.0,
				MaxReturn20d: 20.0,
				Weight5d:     0.4,
			}),
			BaseWeight: 0.30,
		},
		registry.Entry{
			Alpha: reversal.New(reversal.Config{
				ThresholdPct: 0.03, // EMA偏离 3% → 满分反转信号
				MaxReturn5d:  10.0,
				WeightMA:     0.6,
			}),
			BaseWeight: 0.25,
		},
		registry.Entry{
			Alpha: breakout.New(breakout.Config{
				BreakoutThreshold: 8.0, // 5tick涨幅 8% → 满分突破信号
				RefVolume:         500_000,
			}),
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

	// ── 防垄断 + 稳定信号 ─────────────────────────────────────────────────────
	antimono := dampener.New(dampener.Config{
		MaxTop1Streak: 3,
		DampenFactor:  0.6,
	})
	stab := stability.New(stability.Config{
		TopN:           2,
		MinConsecutive: 2,
	})

	// ── 三态市场过滤器 ────────────────────────────────────────────────────────
	//  UPTREND   (偏离 MA +0.5%)  → 正常开仓
	//  OSCILLATE (MA ±0.5%)       → 高质量信号才开仓（Score ≥ 0.30）
	//  DOWNTREND (偏离 MA -0.5%)  → 完全禁止开仓
	marketFilter := trend.New(trend.Config{
		Period:             8,
		UptrendThreshold:   0.005,
		DowntrendThreshold: 0.005,
	})

	// ── PortfolioDecision ────────────────────────────────────────────────────
	//  BuyThreshold 可被 AdaptiveOptimizer 在运行时调高（WinRate 过低时）
	portDecision := topn.New(topn.Config{
		MaxPositions: 3,
		TopN:         3,
		BuyThreshold: 0.08,
	})

	// ── 盈利管理 ──────────────────────────────────────────────────────────────
	//  STOP_LOSS   pnl ≤ -5%       → 硬触发
	//  TRAIL_STOP  pnl ≥ +6% 后激活 → 回撤 2% 触发（主要出场通道）
	//  TAKE_PROFIT pnl ≥ +30%      → 极强势天花板
	posMgr := position.New(position.Config{
		StopLossPct:   0.05,
		TakeProfitPct: 0.30,
		TrailStart:    0.06,
		TrailDrop:     0.02,
	})

	// ── 资金管理 ──────────────────────────────────────────────────────────────
	//  MaxTotalPct 可被 AdaptiveOptimizer 在运行时降低（回撤过大时）
	portMgr := portfolio.New(portfolio.Config{
		TotalCapital: 100_000,
		MaxPositions: 3,
		MaxSinglePct: 0.30,
		MaxTotalPct:  0.80,
		RankPcts:     []float64{0.40, 0.30, 0.30},
	})

	exec := simulated.New(simulated.Config{
		SlippagePct:   0,
		CommissionPct: 0,
	})

	// ── 执行纪律控制 ──────────────────────────────────────────────────────────
	execCtrl := execctrl.New(execctrl.Config{
		CooldownTicksLoss:   5,
		CooldownTicksProfit: 3,
		HighPriceBlockTicks: 20,
		MinHoldTicks:        3,
		MaxBuyPerTick:       2,
		MaxSellPerTick:      2,
	})

	// ── 绩效追踪（每 20 tick 打印一次报告）───────────────────────────────────
	perfTracker := performance.New(performance.Config{
		InitialCapital:    100_000,
		ReportEveryNTicks: 20,
	})

	tradeLogger := console.New()
	reviewer := weekly.New(tradeLogPath)

	eng := engine.New(
		engine.Config{
			TickInterval:      time.Second,
			ReviewWeekday:     time.Friday,
			ReviewHour:        18,
			LogRank:           true,
			IndexSymbol:       indexSymbol,
			OscillateMinScore: 0.30,
		},
		screener,
		provider,
		alphaReg, // 实现 AlphaEngine + StrategyRegistry（双接口）
		antimono,
		stab,
		marketFilter,
		portDecision,
		posMgr,
		portMgr,
		execCtrl,
		perfTracker,
		exec,
		tradeLogger,
		reviewer,
	)

	// ── 自适应优化器 ──────────────────────────────────────────────────────────
	//
	//  规则 1：DrawdownGuard
	//    MaxDrawdown > 8%  → MaxTotalPct: 80% → 50%
	//    目的：回撤超阈值时自动缩仓，保留现金应对进一步下跌
	//
	//  规则 2：WinRateGuard（至少 5 笔交易后生效）
	//    WinRate < 35%     → BuyThreshold: 0.08 → 0.15
	//    目的：连续亏损时提高入场门槛，减少弱信号交易次数
	eng.SetAdaptiveOptimizer(adaptive.New(adaptive.Config{
		DrawdownThreshold:  8.0,
		WinRateThreshold:   35.0,
		MinTrades:          5,
		NormalMaxTotalPct:  0.80,
		ReducedMaxTotalPct: 0.50,
		NormalBuyThreshold: 0.08,
		RaisedBuyThreshold: 0.15,
	}))

	// ── 运行 200 tick ─────────────────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runCtx, cancel := context.WithTimeout(ctx, demoRunDuration)
	defer cancel()

	if err := eng.Run(runCtx); err != nil && err != context.DeadlineExceeded {
		log.Fatalf("[main] engine error: %v", err)
	}

	log.Println("════════════════════════════════════════════════════════════════")
	log.Println("  验证完成：多策略自适应 / 权重动态变化 / 收益曲线")
	log.Println("════════════════════════════════════════════════════════════════")
}
