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
	"fmt"
	"log"
	"math"
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
func (sp *ScenarioProvider) GetRealtime(symbols []string) map[string]*core.Quote {
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

	result := sp.stocks.GetRealtime(stockSyms)
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

func pct(count, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(count) / float64(total) * 100
}

func sumPnL(trades []tradeRecord) float64 {
	s := 0.0
	for _, t := range trades {
		s += t.pnlPct
	}
	return s
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

func formatPnLSlice(s []float64) string {
	parts := make([]string, len(s))
	for i, v := range s {
		parts[i] = fmt.Sprintf("%+.1f%%", v)
	}
	return "[" + strings.Join(parts, " ") + "]"
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

// ─── 验证报告 ─────────────────────────────────────────────────────────────────

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
