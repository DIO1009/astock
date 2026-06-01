// cmd/validate/main.go
//
// # Regime Engine 完整验证程序
//
// 场景设计（300 tick，4 个明确市场阶段）：
//
//	Phase 1 (tick  1- 80): 牛市  — index 从 100 线性涨至 148（+0.6/tick）→ UPTREND
//	Phase 2 (tick 81-160): 震荡  — index 在 148±0.3 窄幅震荡              → OSCILLATE
//	Phase 3 (tick161-240): 熊市  — index 从 148 线性跌至 100（-0.6/tick）→ DOWNTREND
//	Phase 4 (tick241-300): 恢复  — index 在 100±0.3 震荡修复              → OSCILLATE
//
// 输出 7 项验证报告：
//
//	[1] 市场状态统计
//	[2] 按市场状态的交易表现
//	[3] 策略使用分布
//	[4] 状态内权重快照
//	[5] 收益归因
//	[6] 策略健康状态（Kill Switch）
//	[7] 分市场整体表现 + 最终结论
package main

import (
	"context"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"astock_trade/adaptive"
	"astock_trade/alpha/breakout"
	"astock_trade/alpha/momentum"
	"astock_trade/alpha/registry"
	"astock_trade/alpha/reversal"
	"astock_trade/alpha/volatility"
	"astock_trade/alpha/volume"
	"astock_trade/core"
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

// ─── 全局常量 ─────────────────────────────────────────────────────────────────

const (
	indexSym = "000300"
	logFile  = "validate_trades.jsonl"
	runTicks = 300 // 精确运行 tick 数

	// Kill Switch 参数
	ksWindow    = 5  // 最近 N 笔归因交易滑动窗口
	ksThreshold = 3  // 连续亏损达到此次数 → 关闭策略
	ksCooldown  = 15 // 关闭后冷却 tick 数
)

// ─── 场景行情提供器 ────────────────────────────────────────────────────────────

type phaseSpec struct {
	ticks int
	start float64 // 该阶段起始价格（由 buildPhases 计算）
	delta float64 // 每 tick 线性增量；0 = 震荡
	amp   float64 // 震荡振幅（仅 delta==0 时有效）
	label string
}

// buildPhases 根据各阶段配置计算起始价格，返回完整的阶段序列。
func buildPhases() []phaseSpec {
	specs := []phaseSpec{
		{ticks: 80, start: 100.0, delta: +0.60, label: "BULL"},
		{ticks: 80, delta: 0, amp: 0.30, label: "FLAT"},
		{ticks: 80, delta: -0.60, label: "BEAR"},
		{ticks: 60, delta: 0, amp: 0.30, label: "RECOVERY"},
	}
	// 计算各阶段起始价（趋势阶段推进价格，震荡阶段价格不变）
	price := specs[0].start
	for i := range specs {
		specs[i].start = price
		if specs[i].delta != 0 {
			price += specs[i].delta * float64(specs[i].ticks)
		}
	}
	return specs
}

// ScenarioProvider 为股票标的使用 mock.Provider 的随机游走，
// 为指数标的生成场景化的确定性价格序列。
// 当 tick 超过 runTicks 时自动取消 context，确保精确运行 300 tick。
type ScenarioProvider struct {
	stocks      *mock.Provider
	mu          sync.Mutex
	tick        int
	phases      []phaseSpec
	phaseIdx    int
	tickInPhase int
	curPrice    float64
	cancelFn    context.CancelFunc // 300 tick 后自动停止
}

func newScenarioProvider(cancelFn context.CancelFunc) *ScenarioProvider {
	ph := buildPhases()
	return &ScenarioProvider{
		stocks:   mock.New(),
		phases:   ph,
		curPrice: ph[0].start,
		cancelFn: cancelFn,
	}
}

// GetRealtime 每次调用推进指数价格一步，股票价格委托内部 mock.Provider 生成。
// 当 tick 超过 runTicks 时触发 cancelFn，精确控制回测长度。
func (sp *ScenarioProvider) GetRealtime(ctx context.Context, symbols []string) map[string]*core.Quote {
	sp.mu.Lock()
	sp.tick++
	if sp.tick > runTicks && sp.cancelFn != nil {
		sp.cancelFn()
	}

	ph := sp.phases[sp.phaseIdx]
	sp.tickInPhase++

	if ph.delta != 0 {
		sp.curPrice += ph.delta
	} else {
		// 奇偶交替震荡
		if sp.tickInPhase%2 == 1 {
			sp.curPrice = ph.start + ph.amp
		} else {
			sp.curPrice = ph.start - ph.amp
		}
	}

	// 阶段切换
	if sp.tickInPhase >= ph.ticks && sp.phaseIdx < len(sp.phases)-1 {
		sp.phaseIdx++
		sp.tickInPhase = 0
		sp.curPrice = sp.phases[sp.phaseIdx].start
	}

	indexPrice := sp.curPrice
	sp.mu.Unlock()

	var stockSyms []string
	wantIdx := false
	for _, s := range symbols {
		if s == indexSym {
			wantIdx = true
		} else {
			stockSyms = append(stockSyms, s)
		}
	}

	result := sp.stocks.GetRealtime(ctx, stockSyms)
	if wantIdx {
		spread := indexPrice * 0.001
		result[indexSym] = &core.Quote{
			Symbol:    indexSym,
			Price:     indexPrice,
			PrevClose: indexPrice,
			Bid1:      indexPrice - spread,
			Ask1:      indexPrice + spread,
			Volume:    10_000_000,
			Timestamp: time.Now().UnixMilli(),
		}
	}
	return result
}

// ─── Regime 追踪器 ────────────────────────────────────────────────────────────

// RegimeTracker 包裹 core.MarketFilter，记录每 tick 的 Regime 统计。
type RegimeTracker struct {
	inner   core.MarketFilter
	mu      sync.Mutex
	counts  [3]int // indexed by core.MarketState (0=Up,1=Osc,2=Down)
	current core.MarketState
}

func newRegimeTracker(inner core.MarketFilter) *RegimeTracker {
	return &RegimeTracker{inner: inner, current: core.MarketOscillate}
}

func (rt *RegimeTracker) State(q *core.Quote) core.MarketState {
	s := rt.inner.State(q)
	rt.mu.Lock()
	rt.current = s
	rt.counts[s]++
	rt.mu.Unlock()
	return s
}

func (rt *RegimeTracker) AllowOpen(q *core.Quote) bool {
	return rt.inner.AllowOpen(q)
}

// Current 返回最近一次 State() 记录的市场状态。
func (rt *RegimeTracker) Current() core.MarketState {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.current
}

// Stats 返回三种状态的 tick 计数。
func (rt *RegimeTracker) Stats() (up, osc, down int) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.counts[core.MarketUptrend],
		rt.counts[core.MarketOscillate],
		rt.counts[core.MarketDowntrend]
}

// ─── 工具化绩效追踪器 ──────────────────────────────────────────────────────────

// tradeRecord 保存一笔完整交易的 Regime 上下文。
type tradeRecord struct {
	symbol   string
	regime   core.MarketState
	pnlPct   float64
	exitType string
}

// InstrumentedPerfTracker 包裹 core.PerformanceTracker，
// 为每笔交易标注入场时的市场 Regime，并按 Regime 收集权益曲线用于回撤计算。
type InstrumentedPerfTracker struct {
	inner       core.PerformanceTracker
	regimeTrk   *RegimeTracker
	mu          sync.Mutex
	entryRegime map[string]core.MarketState // symbol → 入场时的 Regime
	trades      []tradeRecord
	equityByReg [3][]float64 // 按 Regime 分段存储权益值
}

func newInstrumentedPerfTracker(inner core.PerformanceTracker, rt *RegimeTracker) *InstrumentedPerfTracker {
	return &InstrumentedPerfTracker{
		inner:       inner,
		regimeTrk:   rt,
		entryRegime: make(map[string]core.MarketState),
	}
}

func (ip *InstrumentedPerfTracker) OnBuy(trade *core.Trade) {
	regime := ip.regimeTrk.Current()
	ip.mu.Lock()
	ip.entryRegime[trade.Symbol] = regime
	ip.mu.Unlock()
	ip.inner.OnBuy(trade)
}

func (ip *InstrumentedPerfTracker) OnSell(trade *core.Trade, entryAvg float64, holdTicks int, exitType string) {
	ip.inner.OnSell(trade, entryAvg, holdTicks, exitType)
	pnlPct := 0.0
	if entryAvg > 0 {
		pnlPct = (trade.Price - entryAvg) / entryAvg * 100
	}
	ip.mu.Lock()
	regime := ip.entryRegime[trade.Symbol]
	delete(ip.entryRegime, trade.Symbol)
	ip.trades = append(ip.trades, tradeRecord{
		symbol:   trade.Symbol,
		regime:   regime,
		pnlPct:   pnlPct,
		exitType: exitType,
	})
	ip.mu.Unlock()
}

func (ip *InstrumentedPerfTracker) RecordEquity(equity float64) {
	regime := ip.regimeTrk.Current()
	ip.mu.Lock()
	ip.equityByReg[regime] = append(ip.equityByReg[regime], equity)
	ip.mu.Unlock()
	ip.inner.RecordEquity(equity)
}

func (ip *InstrumentedPerfTracker) MaybeReport(tick int)              { ip.inner.MaybeReport(tick) }
func (ip *InstrumentedPerfTracker) Report() core.PerformanceReport    { return ip.inner.Report() }
func (ip *InstrumentedPerfTracker) Cash() float64                     { return ip.inner.Cash() }
func (ip *InstrumentedPerfTracker) ClosedTrades() []core.ClosedTrade  { return ip.inner.ClosedTrades() }

// TradesByRegime 将所有已完成交易按入场 Regime 分组返回。
func (ip *InstrumentedPerfTracker) TradesByRegime() map[core.MarketState][]tradeRecord {
	ip.mu.Lock()
	defer ip.mu.Unlock()
	result := make(map[core.MarketState][]tradeRecord)
	for _, t := range ip.trades {
		result[t.regime] = append(result[t.regime], t)
	}
	return result
}

// MaxDrawdownByRegime 计算每个 Regime 阶段内的最大回撤。
func (ip *InstrumentedPerfTracker) MaxDrawdownByRegime() [3]float64 {
	ip.mu.Lock()
	defer ip.mu.Unlock()
	var out [3]float64
	for i := range out {
		out[i] = calcMaxDrawdown(ip.equityByReg[i])
	}
	return out
}

// ─── 工具化策略注册表（含 Kill Switch）────────────────────────────────────────

type regimeStratKey struct {
	regime core.MarketState
	strat  string
}

type stratRegimeStat struct {
	trades   int
	wins     int
	totalPnL float64
}

type entryMeta struct {
	regime core.MarketState
	strat  string
}

type killState struct {
	recentPnL    []float64 // 最近 ksWindow 笔归因交易的 pnl%
	disabledTick int       // 禁用到此 tick（0 = 未禁用）
	everDisabled bool      // 是否曾被禁用过（用于报告）
}

// InstrumentedRegistry 包裹 core.StrategyRegistry，增加：
//  1. 按（Regime × Strategy）统计交易分布
//  2. Kill Switch：连续亏损 ksThreshold 次 → 禁用该策略 ksCooldown tick
type InstrumentedRegistry struct {
	inner       core.StrategyRegistry
	regimeTrk   *RegimeTracker
	mu          sync.Mutex
	currentTick int
	openMeta    map[string]*entryMeta
	regimeStats map[regimeStratKey]*stratRegimeStat
	ksState     map[string]*killState
}

func newInstrumentedRegistry(inner core.StrategyRegistry, rt *RegimeTracker) *InstrumentedRegistry {
	return &InstrumentedRegistry{
		inner:       inner,
		regimeTrk:   rt,
		openMeta:    make(map[string]*entryMeta),
		regimeStats: make(map[regimeStratKey]*stratRegimeStat),
		ksState:     make(map[string]*killState),
	}
}

// Rank 委托内部注册表评分，然后将已禁用策略的贡献清零并重新计算合成分。
func (ir *InstrumentedRegistry) Rank(quotes map[string]*core.Quote) []core.Signal {
	ir.mu.Lock()
	ir.currentTick++
	disabled := ir.disabledNow()
	ir.mu.Unlock()

	signals := ir.inner.Rank(quotes)

	if len(disabled) == 0 {
		return signals
	}

	for i := range signals {
		sig := &signals[i]
		modified := false
		for strat := range disabled {
			if _, ok := sig.Breakdown[strat]; ok {
				sig.Breakdown[strat] = 0
				modified = true
			}
		}
		if modified {
			total, count := 0.0, 0
			for _, v := range sig.Breakdown {
				total += v
				count++
			}
			if count > 0 {
				sig.Score = total / float64(count)
			}
		}
	}
	sort.Slice(signals, func(i, j int) bool {
		return signals[i].Score > signals[j].Score
	})
	return signals
}

// RecordBuy 记录入场时的 Regime 和主导策略，供 RecordSell 归因使用。
func (ir *InstrumentedRegistry) RecordBuy(sym string, breakdown map[string]float64) {
	ir.inner.RecordBuy(sym, breakdown)
	dom := dominantStrat(breakdown)
	regime := ir.regimeTrk.Current()
	ir.mu.Lock()
	ir.openMeta[sym] = &entryMeta{regime: regime, strat: dom}
	ir.mu.Unlock()
}

// RecordSell 更新 Regime×Strategy 统计，并触发 Kill Switch 检查。
func (ir *InstrumentedRegistry) RecordSell(sym string, pnlPct float64) {
	ir.inner.RecordSell(sym, pnlPct)
	ir.mu.Lock()
	defer ir.mu.Unlock()

	meta, ok := ir.openMeta[sym]
	if !ok {
		return
	}
	delete(ir.openMeta, sym)

	// 更新 Regime × Strategy 统计
	key := regimeStratKey{meta.regime, meta.strat}
	st, exists := ir.regimeStats[key]
	if !exists {
		st = &stratRegimeStat{}
		ir.regimeStats[key] = st
	}
	st.trades++
	st.totalPnL += pnlPct
	if pnlPct > 0 {
		st.wins++
	}

	// Kill Switch：更新滑动窗口并检查连续亏损
	if meta.strat == "" {
		return
	}
	ks, exists := ir.ksState[meta.strat]
	if !exists {
		ks = &killState{}
		ir.ksState[meta.strat] = ks
	}
	ks.recentPnL = append(ks.recentPnL, pnlPct)
	if len(ks.recentPnL) > ksWindow {
		ks.recentPnL = ks.recentPnL[1:]
	}

	// 统计尾部连续亏损次数
	consec := 0
	for i := len(ks.recentPnL) - 1; i >= 0; i-- {
		if ks.recentPnL[i] < 0 {
			consec++
		} else {
			break
		}
	}
	if consec >= ksThreshold && ir.currentTick > ks.disabledTick {
		ks.disabledTick = ir.currentTick + ksCooldown
		ks.everDisabled = true
		log.Printf("  🔴 [KillSwitch] %-12s DISABLED %d ticks (consec_loss=%d  pnl_window=%v)",
			meta.strat, ksCooldown, consec, formatPnLSlice(ks.recentPnL))
	}
}

func (ir *InstrumentedRegistry) WeightSnapshot() []core.StrategyWeight {
	return ir.inner.WeightSnapshot()
}

// disabledNow 返回当前 tick 仍处于禁用状态的策略集合。调用前需持锁。
func (ir *InstrumentedRegistry) disabledNow() map[string]struct{} {
	out := make(map[string]struct{})
	for strat, ks := range ir.ksState {
		if ir.currentTick < ks.disabledTick {
			out[strat] = struct{}{}
		}
	}
	return out
}

// KillSwitchReport 返回每个策略的 Kill Switch 状态快照。
func (ir *InstrumentedRegistry) KillSwitchReport() map[string]ksStatus {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	out := make(map[string]ksStatus, len(ir.ksState))
	for strat, ks := range ir.ksState {
		remaining := 0
		active := true
		if ir.currentTick < ks.disabledTick {
			active = false
			remaining = ks.disabledTick - ir.currentTick
		}
		out[strat] = ksStatus{
			Active:       active,
			EverDisabled: ks.everDisabled,
			RemainingCD:  remaining,
			RecentPnL:    append([]float64{}, ks.recentPnL...),
		}
	}
	return out
}

// RegimeStratStats 返回 (Regime × Strategy) 统计的副本。
func (ir *InstrumentedRegistry) RegimeStratStats() map[regimeStratKey]*stratRegimeStat {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	out := make(map[regimeStratKey]*stratRegimeStat, len(ir.regimeStats))
	for k, v := range ir.regimeStats {
		cp := *v
		out[k] = &cp
	}
	return out
}

type ksStatus struct {
	Active       bool
	EverDisabled bool
	RemainingCD  int
	RecentPnL    []float64
}

// ─── 辅助函数 ─────────────────────────────────────────────────────────────────

func dominantStrat(breakdown map[string]float64) string {
	best, bestScore := "", 0.0
	for name, score := range breakdown {
		if strings.HasPrefix(name, "_") {
			continue
		}
		if score > bestScore {
			bestScore = score
			best = name
		}
	}
	return best
}

func calcMaxDrawdown(curve []float64) float64 {
	if len(curve) < 2 {
		return 0
	}
	peak, dd := curve[0], 0.0
	for _, v := range curve {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			if d := (peak - v) / peak * 100; d > dd {
				dd = d
			}
		}
	}
	return dd
}

// ─── 主函数 ───────────────────────────────────────────────────────────────────

func main() {
	log.SetFlags(log.Ltime)
	log.Println("══════════ Regime Engine 验证回测（300 tick × 4 阶段）══════════")

	// ── 运行 300 tick ──────────────────────────────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── 场景行情提供器 ─────────────────────────────────────────────────────
	// 将 cancel 传入，使 ScenarioProvider 在恰好 runTicks 个 tick 后自动停止。
	provider := newScenarioProvider(cancel)

	// ── 三态市场过滤器 + 追踪器 ────────────────────────────────────────────
	baseFilter := trend.New(trend.Config{
		Period:             8,
		UptrendThreshold:   0.005,
		DowntrendThreshold: 0.005,
	})
	regimeTrk := newRegimeTracker(baseFilter)

	// ── Alpha 策略注册表 + 工具化包装 ──────────────────────────────────────
	baseReg := registry.New(
		registry.Config{UpdateEvery: 20, Lambda: 0.40, MinFactor: 0.20, MaxFactor: 3.0},
		registry.Entry{
			Alpha:      momentum.New(momentum.Config{MaxReturn5d: 10.0, MaxReturn20d: 20.0, Weight5d: 0.4}),
			BaseWeight: 0.30,
		},
		registry.Entry{
			Alpha:      reversal.New(reversal.Config{ThresholdPct: 0.03, MaxReturn5d: 10.0, WeightMA: 0.6}),
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
	instrReg := newInstrumentedRegistry(baseReg, regimeTrk)

	// ── 绩效追踪器 + 工具化包装 ────────────────────────────────────────────
	basePerf := performance.New(performance.Config{
		InitialCapital:    100_000,
		ReportEveryNTicks: 60, // 每 60 tick 打印一次中间报告
	})
	instrPerf := newInstrumentedPerfTracker(basePerf, regimeTrk)

	// ── 其余组件（与 main.go 完全一致）────────────────────────────────────
	antimono := dampener.New(dampener.Config{MaxTop1Streak: 3, DampenFactor: 0.6})
	stab := stability.New(stability.Config{TopN: 2, MinConsecutive: 2})
	portDecision := topn.New(topn.Config{MaxPositions: 3, TopN: 3, BuyThreshold: 0.08})
	posMgr := position.New(position.Config{
		StopLossPct: 0.05, TakeProfitPct: 0.30, TrailStart: 0.06, TrailDrop: 0.02,
	})
	portMgr := portfolio.New(portfolio.Config{
		TotalCapital: 100_000, MaxPositions: 3, MaxSinglePct: 0.30,
		MaxTotalPct: 0.80, RankPcts: []float64{0.40, 0.30, 0.30},
	})
	exec := simulated.New(simulated.Config{})
	execCtrl := execctrl.New(execctrl.Config{
		CooldownTicksLoss: 5, CooldownTicksProfit: 3,
		HighPriceBlockTicks: 20, MinHoldTicks: 3,
		MaxBuyPerTick: 2, MaxSellPerTick: 2,
	})
	tradeLogger := console.New()
	screener := static.New([]string{"600519", "000858", "300750"})
	reviewer := weekly.New(logFile)

	eng := engine.New(
		engine.Config{
			TickInterval:      5 * time.Millisecond, // 快速运行（300 tick ≈ 1.5s）
			ReviewWeekday:     time.Friday,
			ReviewHour:        18,
			LogRank:           false,
			IndexSymbol:       indexSym,
			OscillateMinScore: 0.25,
		},
		screener,
		provider,
		instrReg, // 实现 AlphaEngine + StrategyRegistry（含 Kill Switch）
		antimono,
		stab,
		regimeTrk, // 实现 MarketFilter（含 Regime 统计）
		portDecision,
		posMgr,
		portMgr,
		execCtrl,
		instrPerf, // 实现 PerformanceTracker（含 Regime 标注）
		exec,
		tradeLogger,
		reviewer,
	)
	eng.SetAdaptiveOptimizer(adaptive.New(adaptive.Config{
		DrawdownThreshold:  8.0,
		WinRateThreshold:   35.0,
		MinTrades:          5,
		NormalMaxTotalPct:  0.80,
		ReducedMaxTotalPct: 0.50,
		NormalBuyThreshold: 0.08,
		RaisedBuyThreshold: 0.15,
	}))

	if err := eng.Run(ctx); err != nil &&
		err != context.DeadlineExceeded &&
		err != context.Canceled {
		log.Fatalf("engine error: %v", err)
	}

	// ── 输出验证报告 ───────────────────────────────────────────────────────
	printValidationReport(regimeTrk, instrPerf, instrReg)
}
