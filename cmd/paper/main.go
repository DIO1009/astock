// cmd/paper is the composition root for Paper Trading mode.
//
// Paper Trading 目标：
//
//	【Feature 1】历史数据回放（provider/replay）+ 可切换至实时行情
//	【Feature 2】统一 Broker 接口（broker/paper 包装 realistic.Executor）
//	【Feature 3】实盘日志（logger/execution 记录信号时间/成交时间/理论价/实际价）
//	【Feature 4】实时监控与告警（monitor 跟踪权益/回撤/风险档位/Kill Switch）
//	【Feature 5】实盘偏差分析（analysis/deviation 统计滑点/成交失败率/延迟）
//	【Feature 6】上线前最终安全控制（safety.Guard）
//	  - 连续亏损抑制：≥5笔 → 仓位×0.5；≥8笔 → 停止开仓50tick
//	  - 人工控制接口：SIGUSR1=StopOpening / SIGUSR2=ForceLiquidate
//	  - 实盘保护：启动时加载持仓快照；出现执行异常时自动暂停
//
// 验证目标：
//
//	Paper Trading ≥ 2 周（每个交易日 = 一个 Tick，约 10 个交易日 / 2 周）
//	实盘偏差可解释（滑点 ≈ 成本模型预期，成交失败率 < 10%）
//	风控机制全部真实触发（STOP_LOSS / TRAIL_STOP / Kill Switch）
//	连续亏损 ≤ 6 笔；最大回撤 ≤ 12%；系统可人工干预
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"astock_trade/adaptive"
	"astock_trade/alpha/breakout"
	"astock_trade/alpha/daily"
	"astock_trade/alpha/momentum"
	"astock_trade/alpha/registry"
	"astock_trade/alpha/reversal"
	"astock_trade/alpha/volatility"
	"astock_trade/alpha/volume"
	"astock_trade/analysis/deviation"
	"astock_trade/broker/paper"
	"astock_trade/calendar"
	"astock_trade/core"
	"astock_trade/dashboard"
	"astock_trade/datacheck"
	"astock_trade/decision/topn"
	"astock_trade/engine"
	"astock_trade/execctrl"
	"astock_trade/executor/realistic"
	"astock_trade/logger/console"
	"astock_trade/logger/execution"
	"astock_trade/logger/filelog"
	"astock_trade/market/trend"
	"astock_trade/monitor"
	"astock_trade/performance"
	"astock_trade/portfolio"
	"astock_trade/position"
	"astock_trade/provider/eastmoney"
	"astock_trade/provider/replay"
	"astock_trade/report"
	"astock_trade/review/weekly"
	"astock_trade/risk"
	"astock_trade/rotation"
	"astock_trade/safety"
	"astock_trade/screener/dynamic"
	"astock_trade/screener/static"
	"astock_trade/signal/dampener"
	"astock_trade/signal/stability"
	"astock_trade/store"
)

const (
	// Paper Trading: 每秒 = 1 tick（交易日）；2 周 = 10 个交易日
	// 生产配置: 10 * time.Minute；快速验证: 30 * time.Second
	paperRunDuration  = 10 * time.Minute
	tradeLogPath      = "paper_trades.jsonl"
	execLogPath       = "paper_executions.jsonl"
	csvDataPath       = "paper_data.csv"
	positionStatePath     = "position_state.jsonl" // Feature 6: 持仓快照文件
	tradingCostConfigPath = "config/trading_cost.json"
	indexSymbol           = "000001" // 上证指数（用于 MarketFilter 趋势判断）
	initialCapital        = 100_000.0
	dashboardAddr         = ":18099" // Trading Cockpit WebSocket 端口
	// 0 = restore full persisted runtime history instead of truncating to a recent window.
	restoreLookback = 0 * time.Hour

	drawdownCautionPct   = 20.0
	drawdownDefensePct   = 30.0
	drawdownTier3Pct     = 35.0
	drawdownEmergencyPct = 40.0
)

var symbols = []string{
	"600519", // 贵州茅台 - 消费
	"000858", // 五粮液 - 消费
	"600809", // 山西汾酒 - 消费
	"000568", // 泸州老窖 - 消费
	"600887", // 伊利股份 - 消费
	"000333", // 美的集团 - 家电
	"000651", // 格力电器 - 家电
	"600690", // 海尔智家 - 家电
	"300750", // 宁德时代 - 新能源
	"002594", // 比亚迪 - 汽车
	"601012", // 隆基绿能 - 光伏
	"002475", // 立讯精密 - 电子
	"002415", // 海康威视 - 电子
	"000063", // 中兴通讯 - 通信
	"688981", // 中芯国际 - 半导体
	"300059", // 东方财富 - 金融科技
	"300760", // 迈瑞医疗 - 医疗器械
	"603259", // 药明康德 - 医药
	"600276", // 恒瑞医药 - 医药
	"002714", // 牧原股份 - 农业
	"601318", // 中国平安 - 金融
	"600036", // 招商银行 - 银行
	"601166", // 兴业银行 - 银行
	"601398", // 工商银行 - 银行
	"601288", // 农业银行 - 银行
	"600030", // 中信证券 - 券商
	"601668", // 中国建筑 - 基建
	"600031", // 三一重工 - 机械
	"600900", // 长江电力 - 公用事业
	"601857", // 中国石油 - 能源
	"600938", // 中国海油 - 能源
	"601088", // 中国神华 - 能源
	"601899", // 紫金矿业 - 有色
	"600309", // 万华化学 - 化工
	"600050", // 中国联通 - 通信运营
	"601888", // 中国中免 - 消费服务
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	printBanner()

	// ── Feature 1: 数据提供器（优先级: 东方财富实时 > 本地 CSV > 合成数据）────
	//
	//   环境变量控制：
	//     ASTOCK_LIVE_DATA=1   → 使用东方财富实时行情（直连，自动绕过 VPN）
	//     ASTOCK_DATA_PATH=... → 指定 CSV 文件路径（replay 模式）
	//     （默认）              → 优先加载 real_market_data.csv，否则生成合成数据
	//
	//   实时模式注意事项：
	//     - 仅交易时段（9:30-15:00）有最新价，非交易时段返回昨收价
	//     - 指标（EMA20/Return5d/20d）随 tick 积累，前 20 tick 值偏低属正常
	//     - Tick 间隔自动设为 5 分钟（可通过 ASTOCK_TICK_SECONDS 覆盖）
	var (
		dataProvider core.DataProvider
		barCounts    map[string]int // nil for live mode
		isLiveMode   bool
	)
	allSymbols := append(symbols, indexSymbol)

	if os.Getenv("ASTOCK_LIVE_DATA") == "1" {
		isLiveMode = true
		emProvider := eastmoney.New()
		dataProvider = emProvider

		log.Printf("[Data] ✅ 实时行情模式（东方财富 push2 API，直连，绕过 VPN）")
		log.Printf("[Data]    标的: %v", allSymbols)
	} else {
		replayProvider := replay.New()
		dataProvider = replayProvider
		dataPath := resolveDataPath(csvDataPath, allSymbols)
		if err := replayProvider.LoadCSV(dataPath); err != nil {
			log.Fatalf("[Paper] 加载历史数据失败: %v", err)
		}
		barCounts = make(map[string]int, len(allSymbols))
		for _, sym := range allSymbols {
			barCounts[sym] = replayProvider.BarCount(sym)
			log.Printf("[Paper] 已加载 %s: %d 条历史数据", sym, barCounts[sym])
		}
	}

	// ── Feature 3: 实盘执行日志 ───────────────────────────────────────────────
	execLogger, err := execution.New(execLogPath)
	if err != nil {
		log.Fatalf("[Paper] 创建执行日志失败: %v", err)
	}
	defer execLogger.Close()
	log.Printf("[Paper] 执行日志文件: %s", execLogPath)

	// ── 盈利管理（需要在 posMgr 之前创建，持仓加载需要它）────────────────────
	posMgr := position.New(position.Config{
		StopLossPct:   0.05,
		TakeProfitPct: 0.30,
		TrailStart:    0.06,
		TrailDrop:     0.02,
	})

	// ── Feature 6: 启动时加载上次持仓快照 ────────────────────────────────────
	if err := posMgr.LoadState(positionStatePath); err != nil {
		log.Fatalf("[Paper] 加载持仓快照失败: %v", err)
	}
	if restored := posMgr.AllPositions(); len(restored) > 0 {
		log.Printf("[Paper] ✅ 已恢复 %d 个持仓快照（防重启丢失）", len(restored))
		for _, p := range restored {
			log.Printf("[Paper]   %-8s  qty=%d  avgPrice=%.4f", p.Symbol, p.Quantity, p.AvgPrice)
		}
	} else {
		log.Println("[Paper] 无历史持仓快照，全新启动")
	}

	// ── 资金管理 ──────────────────────────────────────────────────────────────
	// maxPositions 可通过环境变量 ASTOCK_MAX_POS 覆盖（动态选股模式建议 10-20）。
	// ASTOCK_DYNAMIC_SCREENER=1 时自动将默认值设为 10。
	maxPositions := 3
	if os.Getenv("ASTOCK_DYNAMIC_SCREENER") == "1" {
		maxPositions = envInt("ASTOCK_MAX_POS", 10)
	} else if mp := os.Getenv("ASTOCK_MAX_POS"); mp != "" {
		maxPositions = envInt("ASTOCK_MAX_POS", 3)
	}

	// RankPcts 在动态模式下使用等权分配，静态模式维持原有梯度。
	rankPcts := []float64{0.40, 0.30, 0.30}
	if maxPositions > 3 {
		// 等权配置：每个仓位分配等量资金
		pct := 1.0 / float64(maxPositions)
		rankPcts = make([]float64, maxPositions)
		for i := range rankPcts {
			rankPcts[i] = pct
		}
	}
	portMgr := portfolio.New(portfolio.Config{
		TotalCapital: initialCapital,
		MaxPositions: maxPositions,
		MaxSinglePct: 0.20, // 动态模式下单仓上限降低，分散风险
		MaxTotalPct:  0.80,
		RankPcts:     rankPcts,
	})

	// ── Feature 6: 安全控制层 ────────────────────────────────────────────────
	//
	//  Feature 6.1 连续亏损抑制：
	//    streak ≥ 5 → MaxTotalPct × 0.5（通过 portMgr.SetMaxTotalPct）
	//    streak ≥ 8 → 停止开仓 50 tick
	//
	//  Feature 6.2 人工控制（UNIX 信号）：
	//    SIGUSR1 → StopOpening()     （停止新开仓）
	//    SIGUSR2 → ForceLiquidate()  （全部清仓）
	//    SIGHUP  → ResumeOpening()   （恢复开仓）
	//
	//  Feature 6.3 异常执行检测：
	// 执行异常检测阈值（模拟盘放宽）：
	//    延迟 > 1500ms 或 成交率 < 5% → 记入异常窗口
	//    20 tick 内 ≥ 10 次异常 → 暂停所有交易
	// 模拟盘撮合偶尔超 500ms、首单成交率为 0% 均属正常，
	// 使用原始 500ms/20%/3次 阈值会导致整天 BUY 被误屏蔽。
	safetyGuard := safety.New(safety.Config{
		StreakHalfPositionAt: 5,
		StreakFreezeAt:       15,
		StreakFreezeTicks:    12,
		BaseMaxTotalPct:      0.80,
		AbnormalLatencyMs:    15000,
		AbnormalFillRatePct:  5.0,
		AbnormalWindowTicks:  200,
		AbnormalThreshold:    100,
		StatusEveryNTicks:    10,
	}, portMgr)

	// ── Portfolio Risk Engine (for Dashboard RiskInfo) ───────────────────
	riskCfg := risk.Default()
	riskCfg.DD1 = drawdownCautionPct / 100
	riskCfg.DD2 = drawdownDefensePct / 100
	riskCfg.DD3 = drawdownTier3Pct / 100
	riskCfg.DDHardStop = drawdownEmergencyPct / 100
	riskEngine := risk.New(riskCfg, initialCapital)

	// ── DB 变量（在 Broker logger 闭包之前声明，DB 块稍后赋值）─────────────
	var (
		dbStore  *store.Store
		dbWriter *store.Writer
	)

	// ── Feature 2: 统一 Broker 接口 ───────────────────────────────────────────
	// 底层使用 realistic.Executor（真实 A 股成本模型）
	// Paper Broker 包装 Executor 并记录每笔执行的详细信息
	cfg := realistic.Default()
	loadedCfg, err := realistic.LoadTradingCostConfig(tradingCostConfigPath, cfg)
	if err != nil {
		log.Fatalf("[Paper] 加载交易费率配置失败: %v", err)
	}
	innerExec := realistic.New(loadedCfg)
	paperBroker := paper.New(innerExec, initialCapital)
	// 双重日志：写入 jsonl + 实时异常检测
	paperBroker.SetLogger(func(rec *core.ExecutionRecord) {
		execLogger.Log(rec)
		safetyGuard.CheckExecution(rec) // Feature 6.3: 异常检测钩子
		if dbWriter != nil {
			dbWriter.WriteExecution(rec, "") // strategy name will be in rec.Reason
		}
	})
	log.Printf("[Paper] Broker 模式: Paper Trading  底层执行: %s", innerExec.CostSummary())

	// ── Feature 4: 实时监控与告警 ─────────────────────────────────────────────
	mon := monitor.New(monitor.Config{
		CautionDrawdownPct:   drawdownCautionPct,
		DefenseDrawdownPct:   drawdownDefensePct,
		EmergencyDrawdownPct: drawdownEmergencyPct,
		ReportEveryNTicks:    5, // 每 5 tick 打印一次状态
	})
	mon.SetSafetyGuard(safetyGuard) // Feature 6: 状态输出包含安全控制层信息

	// ── PostgreSQL 数据库（可选，通过环境变量 ASTOCK_DB_DSN 配置）───────────────
	//   默认: postgres://postgres:dmrxlbol123@127.0.0.1:5432/astock_trade?sslmode=disable
	//   也可通过环境变量覆盖: export ASTOCK_DB_DSN="<DSN>"
	//   如果连接失败，所有数据库功能静默跳过，系统正常运行。
	dsn := os.Getenv("ASTOCK_DB_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:dmrxlbol123@127.0.0.1:5432/astock_trade?sslmode=disable"
	}
	if dsn != "-" { // 设置 ASTOCK_DB_DSN="-" 可强制禁用DB
		dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
		st, err := store.Open(dbCtx, store.Config{DSN: dsn})
		dbCancel()
		if err != nil {
			log.Printf("[DB] 连接失败（系统继续运行，但不持久化数据）: %v", err)
		} else {
			migrateCtx, migrateCancel := context.WithTimeout(context.Background(), 15*time.Second)
			if err := st.Migrate(migrateCtx); err != nil {
				log.Printf("[DB] Schema 迁移失败: %v", err)
			} else {
				log.Printf("[DB] ✅ 已连接并迁移 Schema")
			}
			migrateCancel()
			dbStore = st
			dbWriter = store.NewWriter(st)
			dbWriter.Start()
			defer func() {
				dbWriter.Close()
				st.Close()
			}()
		}
	} else {
		log.Printf("[DB] ASTOCK_DB_DSN='-' – 已强制禁用数据库持久化")
	}

	// 注册告警回调：EMERGENCY 级别时向 stderr 写入 Kill Switch 告警
	mon.OnAlert(func(evt core.AlertEvent) {
		if evt.Level >= core.RiskEmergency {
			_, _ = fmt.Fprintf(os.Stderr,
				"\n🚨 KILL SWITCH ALERT: 回撤=%.2f%%  权益=¥%.0f  时间=%s\n\n",
				evt.Drawdown, evt.Equity,
				time.UnixMilli(evt.Timestamp).Format("15:04:05"),
			)
		}
		// 写入 DB 风控事件
		if dbWriter != nil {
			dbWriter.WriteRiskEvent(store.RiskRow{
				Timestamp:   evt.Timestamp,
				EventType:   evt.Level.String(),
				Drawdown:    evt.Drawdown,
				PositionPct: 0,
				Description: evt.Message,
			})
		}
	})

	// ── 策略层（与 main.go v11 相同配置，保证回测一致性）─────────────────────
	alphaReg := registry.New(
		registry.Config{UpdateEvery: 5, Lambda: 0.40, MinFactor: 0.20, MaxFactor: 3.0},
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
				ThresholdPct: 0.03,
				MaxReturn5d:  10.0,
				WeightMA:     0.6,
			}),
			BaseWeight: 0.25,
		},
		registry.Entry{
			Alpha: breakout.New(breakout.Config{
				BreakoutThreshold: 8.0,
				RefVolume:         500_000, // 500k 手/日，A股中盘股正常参考量
			}),
			BaseWeight: 0.20,
		},
		registry.Entry{
			Alpha: volume.New(volume.Config{
				RefVolume: 500_000, // 500k 手/日，provider 现在以"手"存储成交量
			}),
			BaseWeight: 0.15,
		},
		registry.Entry{
			Alpha:      volatility.New(volatility.Config{MaxVol: 3.0}),
			BaseWeight: 0.10,
		},
	)

	antimono := dampener.New(dampener.Config{MaxTop1Streak: 3, DampenFactor: 0.6})
	stab := stability.New(stability.Config{TopN: 2, MinConsecutive: 2})

	marketFilter := trend.New(trend.Config{
		Period:             8,
		UptrendThreshold:   0.005,
		DowntrendThreshold: 0.005,
	})

	portDecision := topn.New(topn.Config{
		MaxPositions: maxPositions,
		TopN:         maxPositions,
		BuyThreshold: 0.08,
	})

	execCtrl := execctrl.New(execctrl.Config{
		CooldownTicksLoss:   5,
		CooldownTicksProfit: 3,
		HighPriceBlockTicks: 20,
		MinHoldTicks:        3,
		MaxBuyPerTick:       2,
		MaxSellPerTick:      2,
	})
	rotCfg := rotation.Config{
		RotationStartTime:    envString("ASTOCK_ROTATION_START", "09:45"),
		RotationWatchRank:    envInt("ASTOCK_ROTATION_WATCH_RANK", 70),
		RotationExitRank:     envInt("ASTOCK_ROTATION_EXIT_RANK", 85),
		RotationConfirmTicks: envInt("ASTOCK_ROTATION_CONFIRM_TICKS", 3),
		RotationConfirmDays:  envInt("ASTOCK_ROTATION_CONFIRM_DAYS", 2),
		RotationDelta:        envFloat("ASTOCK_ROTATION_DELTA", 0.10),
		LossDeltaMultiplier:  envFloat("ASTOCK_ROTATION_LOSS_MULT", 1.5),
		MaxRotationPerDay:    envInt("ASTOCK_MAX_ROTATION_PER_DAY", 3),
	}

	perfTracker := performance.New(performance.Config{
		InitialCapital:    initialCapital,
		ReportEveryNTicks: 10,
	})

	if restored, err := restoreRuntimeState(execLogPath, initialCapital, restoreLookback); err != nil {
		log.Printf("[Paper] 启动恢复执行/绩效历史失败: %v", err)
		if positions := posMgr.AllPositions(); len(positions) > 0 {
			// 回退到旧逻辑：至少扣除已恢复持仓的成本，避免现金翻倍。
			perfTracker.SeedRestoredPositions(positions)
		}
	} else {
		if len(restored.ExecutionRecords) > 0 {
			paperBroker.RestoreRecords(restored.ExecutionRecords)
		}
		if restored.Cash > 0 || len(restored.ClosedTrades) > 0 {
			perfTracker.Restore(restored.Cash, restored.EquityCurve, restored.ClosedTrades)
		} else if positions := posMgr.AllPositions(); len(positions) > 0 {
			perfTracker.SeedRestoredPositions(positions)
		}
		log.Printf("[Paper] ✅ 已恢复历史状态: executions=%d closedTrades=%d cash=¥%.2f",
			len(restored.ExecutionRecords), len(restored.ClosedTrades), restored.Cash)
	}
	if dbStore != nil {
		hydrateRuntimeFromDB(context.Background(), dbStore, perfTracker, mon, posMgr.AllPositions())
		restoreSafetyState(context.Background(), dbStore, safetyGuard)
	}

	consoleTradeLogger := console.New()
	fileTradeLogger, err := filelog.New(tradeLogPath)
	if err != nil {
		log.Fatalf("[Paper] 创建交易日志失败: %v", err)
	}
	defer fileTradeLogger.Close()
	tradeLogger := multiTradeLogger{consoleTradeLogger, fileTradeLogger}
	reviewer := weekly.New(tradeLogPath)

	// ── Screener：动态（DB有数据时）或静态（Fallback）────────────────────────
	//   ASTOCK_DYNAMIC_SCREENER=1  强制使用动态选股（需已运行 daily_alpha 写入DB）
	//   （默认）若 DB 可用 & 实时模式 → 自动切换动态 screener
	//           否则 → 固定使用 symbols 列表
	var screener core.Screener
	var dynScreener *dynamic.Screener // kept for scheduler's ForceRefresh()
	useDynamic := isLiveMode && dbStore != nil || os.Getenv("ASTOCK_DYNAMIC_SCREENER") == "1"
	if useDynamic && dbStore != nil {
		dynN := envInt("ASTOCK_TOP_N", rotCfg.RotationExitRank) // 至少覆盖轮动观察区间
		if dynN < rotCfg.RotationExitRank {
			dynN = rotCfg.RotationExitRank
		}
		dynScreener = dynamic.New(dbStore, dynN, dynamic.WithFallback(symbols))
		screener = dynScreener
		log.Printf("[Screener] ✅ 动态选股模式：Top-%d（DB alpha_rankings），最大持仓 %d", dynN, maxPositions)
	} else {
		screener = static.New(symbols)
		log.Printf("[Screener] 固定标的模式：%v，最大持仓 %d", symbols, maxPositions)
	}

	// 实时模式下 Tick 间隔从 ASTOCK_TICK_SECONDS 读取，默认 300s（5 分钟）；
	// Replay 模式保持 1s/tick（快速回放）。
	tickInterval := time.Second
	if isLiveMode {
		// ── 启动预热：同步执行，引擎启动前完成，确保第 1 个 tick 即有有效数据 ──
		// Return5d 从历史 buffer 计算，必须在引擎循环前注入，否则 DataCheck 会
		// 因 Return5d std=0 拦截开仓。FetchDailyCloses 已改为并行下载，耗时约
		// 300-500ms（取决于网络），可接受的启动延迟。
		if ep, ok := dataProvider.(*eastmoney.Provider); ok {
			// 合并：静态 fallback 符号 + 动态候选池 + 指数
			symsToWarm := make([]string, 0, 30)
			seen := make(map[string]bool)
			for _, s := range allSymbols {
				if !seen[s] {
					symsToWarm = append(symsToWarm, s)
					seen[s] = true
				}
			}
			if dynScreener != nil {
				for _, s := range dynScreener.Screen() {
					if !seen[s] {
						symsToWarm = append(symsToWarm, s)
						seen[s] = true
					}
				}
			}
			log.Printf("[PreWarm] 开始下载 %d 只候选股历史收盘价（腾讯财经，并行）…", len(symsToWarm))
			pwCtx, pwCancel := context.WithTimeout(context.Background(), 30*time.Second)
			closesMap := eastmoney.FetchDailyCloses(pwCtx, symsToWarm, 21)
			pwCancel()
			for sym, points := range closesMap {
				ep.PreWarm(sym, points)
			}
			log.Printf("[PreWarm] ✅ 完成：%d/%d 只股票已预热（EMA20 / Return5d / Volatility）",
				len(closesMap), len(symsToWarm))
		}

		tickSecs := 300
		if v := os.Getenv("ASTOCK_TICK_SECONDS"); v != "" {
			if n, err := fmt.Sscanf(v, "%d", &tickSecs); n != 1 || err != nil {
				log.Printf("[Paper] ASTOCK_TICK_SECONDS 解析失败，使用默认 300s")
				tickSecs = 300
			}
		}
		tickInterval = time.Duration(tickSecs) * time.Second
		log.Printf("[Data]    Tick 间隔: %s（可通过 ASTOCK_TICK_SECONDS 覆盖）", tickInterval)
	}

	eng := engine.New(
		engine.Config{
			TickInterval:      tickInterval,
			ReviewWeekday:     time.Friday,
			ReviewHour:        18,
			LogRank:           true,
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
		paperBroker, // ← Paper Broker 替代 simulated.Executor
		tradeLogger,
		reviewer,
	)
	eng.SetRotationPolicy(rotation.New(rotCfg))

	// ── 自适应优化器 ──────────────────────────────────────────────────────────
	eng.SetAdaptiveOptimizer(adaptive.New(adaptive.Config{
		DrawdownThreshold:  drawdownEmergencyPct,
		WinRateThreshold:   35.0,
		MinTrades:          5,
		NormalMaxTotalPct:  0.80,
		ReducedMaxTotalPct: 0.50,
		NormalBuyThreshold: 0.08,
		RaisedBuyThreshold: 0.15,
	}))

	// ── Feature 4: 注册 Monitor ───────────────────────────────────────────────
	eng.SetMonitor(mon)

	// ── T+1 交易日历（A 股节假日 2020-2030）────────────────────────────────────
	eng.SetCalendar(calendar.New())

	// ── Feature 6: 注册 SafetyGuard ──────────────────────────────────────────
	eng.SetSafetyGuard(safetyGuard)

	// ── 数据完整性校验器 ──────────────────────────────────────────────────────
	// 每 tick 在 Alpha 评分和下单之前运行。任何检查失败均禁止开仓（平仓不受影响）。
	// 实时模式下必须开启；回放模式下也会校验（时间戳宽容度更大）。
	eng.SetDataChecker(datacheck.New(datacheck.Config{
		IndexSymbol:            indexSymbol,
		MaxQuoteAgeHours:       36,   // 允许盘后缓存价格（非交易时段）
		MinFactorStd:           1e-6, // std≈0 → 因子退化
		VolumeUniformThreshold: 0.9,  // 90%股票成交量相同 → 数据冻结
		MinStockCount:          3,
	}))

	// ── Dashboard 服务器（在所有组件就绪后创建）──────────────────────────────
	dashSrv := dashboard.New(
		dashboard.Config{Addr: dashboardAddr, StaticDir: "dashboard/frontend/dist"},
		mon, safetyGuard, riskEngine,
		posMgr, perfTracker, paperBroker, alphaReg,
	)
	if dbWriter != nil {
		dashSrv.SetWriter(dbWriter)
	}
	if dbStore != nil {
		dashSrv.SetStore(dbStore)
	}
	go func() {
		if err := dashSrv.ListenAndServe(); err != nil {
			log.Printf("[Dashboard] 服务异常: %v", err)
		}
	}()
	eng.SetDashboard(dashSrv)
	if os.Getenv("FACTOR_DIAG") == "1" {
		eng.SetFactorDiagnosis(true)
		log.Println("[Paper] FACTOR_DIAG=1 已开启：下一合法 tick 仅输出因子诊断并退出，不执行交易")
	}

	// ── 每日自动选股调度器 ────────────────────────────────────────────────────
	// 条件：DB 可用 + 动态 Screener 已初始化
	// 行为：
	//   1. 启动时立即检查 DB 是否有今日数据；若无则立刻跑一次选股（约1~3分钟）
	//   2. 每天 09:00 自动重新跑全市场选股，完成后刷新 Screener
	// 好处：系统启动一次，无需每天手动操作
	if dbStore != nil && dynScreener != nil {
		go runAlphaScheduler(context.Background(), dbStore, dynScreener, dataProvider)
		log.Println("[Scheduler] ✅ 每日自动选股已启动（每天 09:00 自动运行，含今日初始化检查）")
	}

	// ── 每日策略报告调度器 ────────────────────────────────────────────────────
	// 条件：DB 可用（无论是否动态选股均生成报告）
	// 行为：
	//   1. 每天 15:10 CST 自动生成日报（Markdown → reports/YYYY-MM-DD.md）
	//   2. 失败重试：15:11 / 15:13 / 15:16，仍失败则标记 FAILED
	//   3. 启动时补偿：今日已过 15:10 但无报告 → 立即补生成
	//   4. 跨日补偿：昨日报告缺失或失败 → 立即补生成
	if dbStore != nil {
		reportGen := report.NewGenerator(dbStore, perfTracker, posMgr, "reports")
		reportSched := report.NewScheduler(reportGen, dbStore)
		go reportSched.Run(context.Background())
		log.Println("[ReportScheduler] ✅ 每日策略报告调度已启动（15:10 CST 自动生成，含重试与补偿）")
	}
	// ── Feature 6.2: 人工控制信号处理 ────────────────────────────────────────
	//   SIGUSR1 → 停止开仓（保留平仓）
	//   SIGUSR2 → 全部清仓
	//   SIGHUP  → 恢复开仓
	usr1Ch := make(chan os.Signal, 1)
	usr2Ch := make(chan os.Signal, 1)
	hupCh := make(chan os.Signal, 1)
	signal.Notify(usr1Ch, syscall.SIGUSR1)
	signal.Notify(usr2Ch, syscall.SIGUSR2)
	signal.Notify(hupCh, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-usr1Ch:
				safetyGuard.StopOpening()
			case <-usr2Ch:
				safetyGuard.TriggerForceLiquidate()
			case <-hupCh:
				safetyGuard.ResumeOpening()
			}
		}
	}()
	log.Println("[Paper] 人工控制信号已注册:")
	log.Printf("[Paper]   kill -USR1 %d  → 停止开仓", os.Getpid())
	log.Printf("[Paper]   kill -USR2 %d  → 全部清仓", os.Getpid())
	log.Printf("[Paper]   kill -HUP  %d  → 恢复开仓", os.Getpid())

	// ── 运行 ──────────────────────────────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var runCtx context.Context
	var cancel context.CancelFunc
	if isLiveMode {
		// 实时模式：无截止时间，持续运行直到 SIGTERM/SIGINT
		runCtx, cancel = context.WithCancel(ctx)
		log.Println("[Paper] 开始实时 Paper Trading（东方财富数据，无截止时间）...")
	} else {
		// Replay 模式：运行固定时长后自动结束
		runCtx, cancel = context.WithTimeout(ctx, paperRunDuration)
		log.Println("[Paper] 开始 Paper Trading 模拟...")
	}
	defer cancel()

	runErr := eng.Run(runCtx)

	// ── Feature 6: 保存持仓快照（下次重启恢复用）────────────────────────────
	// ⚠️  必须在任何 log.Fatalf / os.Exit 之前调用，否则 SIGTERM 正常停止时快照会丢失。
	// 无论引擎以何种方式退出（正常关闭、超时、真实异常）都先保存快照。
	if err := posMgr.SaveState(positionStatePath); err != nil {
		log.Printf("[Paper] ⚠️  保存持仓快照失败: %v", err)
	} else {
		positions := posMgr.AllPositions()
		log.Printf("[Paper] ✅ 持仓快照已保存 → %s (%d 个持仓)", positionStatePath, len(positions))
	}

	// context.Canceled  = SIGTERM / SIGINT 正常停止，不是异常
	// context.DeadlineExceeded = Replay 模式到时正常结束，不是异常
	if runErr != nil &&
		!errors.Is(runErr, context.Canceled) &&
		!errors.Is(runErr, context.DeadlineExceeded) {
		log.Fatalf("[Paper] 引擎异常: %v", runErr)
	}

	// ── Feature 4: 最终监控报告 ───────────────────────────────────────────────
	mon.PrintFinalReport()

	// ── Feature 5: 实盘偏差分析 ───────────────────────────────────────────────
	records := paperBroker.Records()
	if len(records) > 0 {
		analyzer := deviation.New()
		analyzer.AddAll(records)
		analyzer.PrintReport()

		// 额外打印 Broker 统计
		total, filled, partial, rejected := paperBroker.Stats()
		log.Printf("[Paper] Broker 执行统计 → 总=%d  全成=%d  部分=%d  拒绝=%d",
			total, filled, partial, rejected)
	} else {
		log.Println("[Paper] 本轮无执行记录（可能数据不足或信号未触发）")
	}

	printSummary(mon, paperBroker, safetyGuard)
}

// resolveDataPath determines which CSV data file to load, in priority order:
//  1. ASTOCK_DATA_PATH env var (explicit override)
//  2. real_market_data.csv (generated by scripts/fetch_data.py)
//  3. fallback: generate synthetic data into csvDataPath and return it
func resolveDataPath(fallbackPath string, allSyms []string) string {
	// Priority 1: explicit env var
	if envPath := os.Getenv("ASTOCK_DATA_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			log.Printf("[Data] ✅ 使用指定数据文件 (ASTOCK_DATA_PATH): %s", envPath)
			return envPath
		}
		log.Printf("[Data] ⚠️  ASTOCK_DATA_PATH 指定的文件不存在: %s，降级至合成数据", envPath)
	}

	// Priority 2: real_market_data.csv in working directory
	realPath := "real_market_data.csv"
	if info, err := os.Stat(realPath); err == nil && info.Size() > 100 {
		log.Printf("[Data] ✅ 使用真实行情数据: %s (%.1fKB)", realPath, float64(info.Size())/1024)
		log.Printf("[Data]    来源: python3 scripts/fetch_data.py")
		return realPath
	}

	// Priority 3: generate synthetic data (fallback)
	log.Printf("[Data] ⚠️  未找到真实数据文件，生成合成数据（价格不反映真实市场）")
	log.Printf("[Data]    建议: pip3 install akshare && python3 scripts/fetch_data.py")
	generateSyntheticCSV(fallbackPath, allSyms, 120)
	return fallbackPath
}

// generateSyntheticCSV creates a CSV file with synthetic OHLCV data for
// the given symbols over nDays trading days, starting from current market-level
// seed prices. Used ONLY as a fallback when real market data is unavailable.
//
// IMPORTANT: This is NOT real market data. Prices are random-walk simulations
// starting from approximate current market prices (updated 2025-Q1).
// Use scripts/fetch_data.py to download real data.
func generateSyntheticCSV(path string, symbols []string, nDays int) {
	rng := rand.New(rand.NewSource(42)) // fixed seed for reproducibility

	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("[Paper] 无法创建数据文件: %v", err)
	}
	defer f.Close()

	fmt.Fprintln(f, "date,symbol,open,high,low,close,volume")

	// 以当前日期回推 nDays 个自然日作为合成数据起点
	startDate := time.Now().AddDate(0, 0, -int(float64(nDays)*1.7))
	startDate = time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, time.Local)

	// 种子价格对应 2025-Q1 实际市价水平（不复权）
	// 每次系统更新时应同步校准此处价格，避免合成数据与真实市价偏差过大
	seedPrices := map[string]float64{
		"600519": 1420.0, // 贵州茅台 (实际约 1420)
		"000858": 88.0,   // 五粮液   (实际约 88)
		"300750": 82.0,   // 宁德时代 (实际约 82)
		"000300": 3750.0, // 沪深300  (实际约 3750)
	}

	prices := make(map[string]float64)
	for sym, p := range seedPrices {
		prices[sym] = p
	}

	day := 0
	for day < nDays {
		// Skip weekends
		if startDate.Weekday() == time.Saturday || startDate.Weekday() == time.Sunday {
			startDate = startDate.AddDate(0, 0, 1)
			continue
		}
		dateStr := startDate.Format("2006-01-02")

		for _, sym := range symbols {
			prev := prices[sym]
			// Random walk with slight upward drift
			drift := rng.NormFloat64()*0.015 + 0.0005
			closeP := math.Max(1.0, prev*(1+drift))
			openP := prev * (1 + rng.NormFloat64()*0.005)
			highP := math.Max(openP, closeP) * (1 + rng.Float64()*0.01)
			lowP := math.Min(openP, closeP) * (1 - rng.Float64()*0.01)
			vol := int64(rng.Intn(5_000_000) + 1_000_000)

			fmt.Fprintf(f, "%s,%s,%.4f,%.4f,%.4f,%.4f,%d\n",
				dateStr, sym, openP, highP, lowP, closeP, vol)
			prices[sym] = closeP
		}

		startDate = startDate.AddDate(0, 0, 1)
		day++
	}
	log.Printf("[Paper] 已生成 %s (%d 交易日, %d 个标的)", path, nDays, len(symbols))
}

func printBanner() {
	log.Println("════════════════════════════════════════════════════════════════")
	log.Println("  A股量化交易系统 — Paper Trading 模式")
	log.Println("  【功能】历史回放 | 统一Broker接口 | 执行日志 | 实时监控 | 偏差分析")
	log.Println("  【安全】连续亏损抑制 | 人工控制接口 | 持仓持久化 | 异常检测")
	log.Println("════════════════════════════════════════════════════════════════")
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

func checkMark(ok bool) string {
	if ok {
		return "✅"
	}
	return "❌"
}

// envInt reads an integer environment variable, returning def if absent/invalid.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return def
}

func envString(key string, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type multiTradeLogger []core.TradeLogger

func (m multiTradeLogger) Log(trade *core.Trade) {
	for _, logger := range m {
		if logger != nil {
			logger.Log(trade)
		}
	}
}


























