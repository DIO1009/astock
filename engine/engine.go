// Package engine orchestrates the real-time trading loop.
package engine

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	"astock_trade/core"
	"astock_trade/datacheck"
	"astock_trade/rotation"
)

// Config holds operational parameters for the engine.
type Config struct {
	TickInterval       time.Duration
	ReviewWeekday      time.Weekday
	ReviewHour         int
	LogRank            bool
	IndexSymbol        string
	OscillateMinScore  float64
}

// Engine is the central orchestrator.
type Engine struct {
	cfg          Config
	screener     core.Screener
	provider     core.DataProvider
	alphaEng     core.AlphaEngine
	adjuster     core.SignalAdjuster
	stabilizer   core.SignalStabilizer
	marketFilter core.MarketFilter
	portDecision core.PortfolioDecision
	posMgr       core.PositionManager
	portMgr      core.PortfolioManager
	execCtrl     core.ExecController
	perfTracker  core.PerformanceTracker
	executor     core.Executor
	tradeLogger  core.TradeLogger
	reviewer     core.Reviewer
	adaptiveOpt  core.AdaptiveOptimizer
	monitor      core.Monitor
	safetyGuard  core.SafetyGuard
	dashboard    core.DashboardReporter
	calendar     core.TradingCalendar
	dataChecker  *datacheck.Checker
	rotationPol  *rotation.Policy
	lastReviewWk int
	tickCount    int

	lastAdaptiveParams core.AdaptiveParams
	factorDiagEnabled  bool
	factorDiagDone     bool
	factorDiagHalted   bool
}

// New constructs an Engine with all required dependencies.
func New(
	cfg Config,
	screener core.Screener,
	provider core.DataProvider,
	alphaEng core.AlphaEngine,
	adjuster core.SignalAdjuster,
	stabilizer core.SignalStabilizer,
	marketFilter core.MarketFilter,
	portDecision core.PortfolioDecision,
	posMgr core.PositionManager,
	portMgr core.PortfolioManager,
	execCtrl core.ExecController,
	perfTracker core.PerformanceTracker,
	executor core.Executor,
	tradeLogger core.TradeLogger,
	reviewer core.Reviewer,
) *Engine {
	return &Engine{
		cfg:          cfg,
		screener:     screener,
		provider:     provider,
		alphaEng:     alphaEng,
		adjuster:     adjuster,
		stabilizer:   stabilizer,
		marketFilter: marketFilter,
		portDecision: portDecision,
		posMgr:       posMgr,
		portMgr:      portMgr,
		execCtrl:     execCtrl,
		perfTracker:  perfTracker,
		executor:     executor,
		tradeLogger:  tradeLogger,
		reviewer:     reviewer,
		rotationPol:  rotation.New(rotation.DefaultConfig()),
		lastReviewWk: -1,
	}
}

func (e *Engine) SetRotationPolicy(p *rotation.Policy) { e.rotationPol = p }
func (e *Engine) SetCalendar(cal core.TradingCalendar) { e.calendar = cal }
func (e *Engine) SetDataChecker(dc *datacheck.Checker) { e.dataChecker = dc }
func (e *Engine) SetAdaptiveOptimizer(opt core.AdaptiveOptimizer) { e.adaptiveOpt = opt }
func (e *Engine) SetMonitor(m core.Monitor) { e.monitor = m }
func (e *Engine) SetSafetyGuard(g core.SafetyGuard) { e.safetyGuard = g }
func (e *Engine) SetDashboard(d core.DashboardReporter) { e.dashboard = d }
func (e *Engine) SetFactorDiagnosis(enabled bool) { e.factorDiagEnabled = enabled }

// Run starts the trading loop and blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	interval := e.cfg.TickInterval
	if interval <= 0 {
		interval = time.Minute
	}
	tradeTicker := time.NewTicker(interval)
	defer tradeTicker.Stop()

	reviewTicker := time.NewTicker(time.Minute)
	defer reviewTicker.Stop()

	log.Println("[Engine] trading loop started")
	now := time.Now()
	if e.inTradingHours(now) {
		e.processTick()
		if e.factorDiagHalted {
			log.Println("[Engine] 因子诊断完成，按配置退出")
			return nil
		}
	} else {
		log.Printf("[Engine] 启动时处于非交易时段（%s），首个 Tick 延后到合法交易窗口", now.In(cstZone).Format("01-02 15:04:05"))
		e.refreshDashboardQuotes()
	}

	skipLoggedHour := -1
	for {
		select {
		case <-ctx.Done():
			log.Println("[Engine] shutting down gracefully")
			return ctx.Err()
		case <-tradeTicker.C:
			now := time.Now()
			if !e.inTradingHours(now) {
				h := now.In(cstZone).Hour()
				if h != skipLoggedHour {
					skipLoggedHour = h
					log.Printf("[Engine] 非交易时段（%s），跳过 Tick — 下一交易时段 09:30 CST", now.In(cstZone).Format("01-02 15:04"))
				}
				e.refreshDashboardQuotes()
				continue
			}
			skipLoggedHour = -1
			e.processTick()
			if e.factorDiagHalted {
				log.Println("[Engine] 因子诊断完成，按配置退出")
				return nil
			}
		case <-reviewTicker.C:
			e.maybeReview()
		}
	}
}

var cstZone = time.FixedZone("CST", 8*3600)

func (e *Engine) inTradingHours(t time.Time) bool {
	if e.calendar == nil {
		return true
	}
	return e.calendar.IsInTradingHours(t)
}

func (e *Engine) refreshDashboardQuotes() {
	if e.dashboard == nil || e.provider == nil || e.posMgr == nil || e.perfTracker == nil {
		return
	}
	positions := e.posMgr.AllPositions()
	if len(positions) == 0 && e.cfg.IndexSymbol == "" {
		e.dashboard.OnQuoteRefresh(e.perfTracker.Cash(), e.perfTracker.Report(), positions, map[string]*core.Quote{})
		return
	}
	symbolSet := make(map[string]bool, len(positions)+1)
	symbols := make([]string, 0, len(positions)+1)
	for _, pos := range positions {
		if pos.Symbol == "" || symbolSet[pos.Symbol] {
			continue
		}
		symbolSet[pos.Symbol] = true
		symbols = append(symbols, pos.Symbol)
	}
	if e.cfg.IndexSymbol != "" && !symbolSet[e.cfg.IndexSymbol] {
		symbolSet[e.cfg.IndexSymbol] = true
		symbols = append(symbols, e.cfg.IndexSymbol)
	}
	quotes := e.provider.GetRealtime(symbols)
	equity := e.perfTracker.Cash()
	for _, pos := range positions {
		if q, ok := quotes[pos.Symbol]; ok && q != nil {
			equity += float64(pos.Quantity) * q.Price
		} else {
			equity += float64(pos.Quantity) * pos.AvgPrice
		}
	}
	e.dashboard.OnQuoteRefresh(equity, e.perfTracker.Report(), positions, quotes)
}

func (e *Engine) tradeDaySeq() int64 {
	if e.calendar != nil {
		return e.calendar.TradeDaySeq(time.Now())
	}
	return int64(e.tickCount)
}

// processTick executes one full trading iteration. It is called only inside valid trading hours.
func (e *Engine) processTick() {
	e.tickCount++
	if e.execCtrl != nil { e.execCtrl.AdvanceTick() }
	if e.safetyGuard != nil { e.safetyGuard.AdvanceTick() }
	tradeDay := e.tradeDaySeq()
	if e.posMgr != nil { e.posMgr.AdvanceTradeDay(tradeDay) }

	if e.factorDiagEnabled && !e.factorDiagDone {
		e.factorDiagDone = true
		e.factorDiagHalted = true
		log.Println("[FactorDiag] START same-tick factor diagnosis; trading disabled for this tick")
		log.Println("[FactorDiag] END diagnosis; engine will exit without placing orders")
		return
	}
	if e.screener == nil || e.provider == nil || e.posMgr == nil {
		return
	}

	stockSymbols := e.screener.Screen()
	posSymSet := make(map[string]bool)
	for _, p := range e.posMgr.AllPositions() { posSymSet[p.Symbol] = true }
	fetchSet := make(map[string]bool, len(stockSymbols)+len(posSymSet))
	for _, s := range stockSymbols { fetchSet[s] = true }
	for s := range posSymSet { fetchSet[s] = true }
	fetchSymbols := make([]string, 0, len(fetchSet)+1)
	for s := range fetchSet { if s != "" { fetchSymbols = append(fetchSymbols, s) } }
	if e.cfg.IndexSymbol != "" { fetchSymbols = append(fetchSymbols, e.cfg.IndexSymbol) }
	allQuotes := e.provider.GetRealtime(fetchSymbols)
	stockQuotes := make(map[string]*core.Quote, len(fetchSet))
	for sym := range fetchSet { if q := allQuotes[sym]; q != nil { stockQuotes[sym] = q } }
	var indexQuote *core.Quote
	if e.cfg.IndexSymbol != "" { indexQuote = allQuotes[e.cfg.IndexSymbol] }

	dataCheckOK := true
	if e.dataChecker != nil {
		result := e.dataChecker.Check(stockQuotes, indexQuote)
		for _, fs := range result.Factors { log.Printf("[DataCheck] 因子统计  %s", fs) }
		for _, w := range result.Warnings { log.Print(w) }
		if !result.OK {
			dataCheckOK = false
			for _, er := range result.Errors { log.Print(er) }
			log.Printf("[DataCheck] ⛔ %d 项校验失败 — 本 tick 禁止开仓，平仓正常执行", len(result.Errors))
		}
	}

	positions := e.posMgr.AllPositions()
	if e.cfg.LogRank && e.portMgr != nil { e.logPortfolioStats(e.portMgr.Stats(positions), positions, stockQuotes) }

	if e.safetyGuard != nil && e.safetyGuard.ShouldForceLiquidate() {
		e.safetyGuard.AcknowledgeForceLiquidate()
		for _, pos := range positions {
			q := stockQuotes[pos.Symbol]
			if q == nil || pos.SellableQty <= 0 { continue }
			holdTicks := 0
			if e.execCtrl != nil { holdTicks = e.execCtrl.GetHoldTicks(pos.Symbol) }
			trade := e.executeOrder(core.Order{Symbol: pos.Symbol, Side: "SELL", Price: q.Bid1, Quantity: pos.SellableQty, Reason: "FORCE_LIQUIDATE"}, stockQuotes)
			if e.execCtrl != nil { e.execCtrl.RecordSell(pos.Symbol, q.Price, "STOP_LOSS") }
			if trade != nil {
				pnlPct := (trade.Price - pos.AvgPrice) / pos.AvgPrice * 100
				if e.perfTracker != nil { e.perfTracker.OnSell(trade, pos.AvgPrice, holdTicks, "STOP_LOSS") }
				e.safetyGuard.OnTradeClosed(pnlPct)
				if reg, ok := e.alphaEng.(core.StrategyRegistry); ok { reg.RecordSell(pos.Symbol, pnlPct) }
			}
		}
	}

	for _, sym := range stockSymbols {
		q := stockQuotes[sym]
		if q == nil { continue }
		pos, held := e.posMgr.GetPosition(sym)
		if !held { continue }
		e.posMgr.UpdateHighest(sym, q.Price)
		exitType := e.posMgr.CheckExit(pos, q)
		if exitType == "HOLD" || pos.SellableQty <= 0 { continue }
		if e.execCtrl != nil && !e.execCtrl.AllowSell(sym, exitType) { continue }
		holdTicks := 0
		if e.execCtrl != nil { holdTicks = e.execCtrl.GetHoldTicks(sym) }
		pnlPct := (q.Price - pos.AvgPrice) / pos.AvgPrice * 100
		trade := e.executeOrder(core.Order{Symbol: sym, Side: "SELL", Price: q.Bid1, Quantity: pos.SellableQty, Reason: fmt.Sprintf("%-12s avg=%8.4f now=%8.4f pnl=%+.2f%%", exitType, pos.AvgPrice, q.Price, pnlPct)}, stockQuotes)
		if e.execCtrl != nil { e.execCtrl.RecordSell(sym, q.Price, exitType) }
		if trade != nil {
			if e.perfTracker != nil { e.perfTracker.OnSell(trade, pos.AvgPrice, holdTicks, exitType) }
			if e.safetyGuard != nil { e.safetyGuard.OnTradeClosed(pnlPct) }
			if reg, ok := e.alphaEng.(core.StrategyRegistry); ok { reg.RecordSell(sym, pnlPct) }
		}
	}

	marketAllows := true
	mktState := core.MarketOscillate
	if e.marketFilter != nil {
		mktState = e.marketFilter.State(indexQuote)
		marketAllows = e.marketFilter.AllowOpen(indexQuote)
	}
	if setter, ok := e.alphaEng.(interface{ SetMarketState(core.MarketState) }); ok { setter.SetMarketState(mktState) }
	if marketReporter, ok := e.dashboard.(core.DashboardMarketReporter); ok {
		indexPrice := 0.0
		if indexQuote != nil {
			indexPrice = indexQuote.Price
		}
		marketReporter.SetMarketState(mktState.String(), indexPrice)
	}

	var signals []core.Signal
	if e.alphaEng != nil { signals = e.alphaEng.Rank(stockQuotes) }
	if e.adjuster != nil { signals, _ = e.adjuster.Adjust(signals) }
	if e.cfg.LogRank { e.logRanking(signals, nil) }
	stableSignals := signals
	var stabilityCounts map[string]int
	if e.stabilizer != nil {
		stableSignals, stabilityCounts = e.stabilizer.Stabilize(signals)
		if e.cfg.LogRank { e.logStability(stabilityCounts, stableSignals) }
	}
	if e.dashboard != nil {
		if dw, ok := e.dashboard.(interface{ SetWatchList(map[string]int) }); ok { dw.SetWatchList(stabilityCounts) }
		if dw, ok := e.dashboard.(interface{ SetSignalCache([]core.Signal) }); ok { dw.SetSignalCache(signals) }
	}

	if e.rotationPol != nil { e.processRotation(signals, stableSignals, stockQuotes, tradeDay) }

	regimeMinScore, regimeMinSource, _ := e.regimeMinScore(signals, mktState, e.cfg.OscillateMinScore)
	if e.cfg.LogRank { log.Printf("[MinScore] regime=%s minScore=%s=%.4f selected=%d/%d", mktState.String(), regimeMinSource, regimeMinScore, countSignalsAtLeast(signals, regimeMinScore), len(signals)) }

	safetyAllowsOpen := true
	if e.safetyGuard != nil && !e.safetyGuard.AllowOpen() { safetyAllowsOpen = false }
	if marketAllows && safetyAllowsOpen && dataCheckOK && e.portMgr != nil && e.portDecision != nil {
		positions = e.posMgr.AllPositions()
		if e.portMgr.CanOpenPosition(positions) {
			buySignals := stableSignals
			if (mktState == core.MarketOscillate || mktState == core.MarketUptrend) && regimeMinScore > 0 {
				filtered := make([]core.Signal, 0, len(stableSignals))
				for _, sig := range stableSignals { if sig.Score >= regimeMinScore { filtered = append(filtered, sig) } }
				buySignals = filtered
			}
			allocations := e.portMgr.AllocatePlan(positions, len(buySignals))
			orders := e.portDecision.Decide(buySignals, stockQuotes, positions, allocations)
			sigMap := make(map[string]core.Signal, len(buySignals))
			for _, sig := range buySignals { sigMap[sig.Symbol] = sig }
			for _, order := range orders {
				if e.posMgr.HasPosition(order.Symbol) || !e.portMgr.CanOpenPosition(e.posMgr.AllPositions()) { continue }
				if e.execCtrl != nil {
					if q := stockQuotes[order.Symbol]; q != nil {
						if allowed, reason := e.execCtrl.AllowBuy(order.Symbol, q.Price); !allowed { log.Printf("  🚫 [ExecCtrl] BUY %-8s BLOCKED  %s", order.Symbol, reason); continue }
					}
				}
				trade := e.executeOrder(order, stockQuotes)
				if trade != nil {
					if e.execCtrl != nil { if q := stockQuotes[order.Symbol]; q != nil { e.execCtrl.RecordBuy(order.Symbol, q.Price) } }
					if e.perfTracker != nil { e.perfTracker.OnBuy(trade) }
					if reg, ok := e.alphaEng.(core.StrategyRegistry); ok { if sig, found := sigMap[order.Symbol]; found { reg.RecordBuy(order.Symbol, sig.Breakdown) } }
				}
			}
		}
	}

	if e.cfg.LogRank { e.logPnL(stockQuotes) }
	if e.perfTracker != nil {
		positions = e.posMgr.AllPositions()
		equity := e.perfTracker.Cash()
		for _, pos := range positions {
			if q := stockQuotes[pos.Symbol]; q != nil { equity += float64(pos.Quantity) * q.Price } else { equity += float64(pos.Quantity) * pos.AvgPrice }
		}
		e.perfTracker.RecordEquity(equity)
		e.perfTracker.MaybeReport(e.tickCount)
		if e.monitor != nil { e.monitor.Update(equity, e.perfTracker.Report(), positions) }
		if e.dashboard != nil { e.dashboard.OnTick(equity, e.perfTracker.Report(), positions, stockQuotes) }
	}
	if e.adaptiveOpt != nil && e.perfTracker != nil {
		params := e.adaptiveOpt.Params(e.perfTracker.Report())
		if setter, ok := e.portMgr.(core.MaxTotalPctSetter); ok { setter.SetMaxTotalPct(params.MaxTotalPct) }
		if setter, ok := e.portDecision.(core.BuyThresholdSetter); ok { setter.SetBuyThreshold(params.BuyThreshold) }
		if params.LogReason != "" && (params.MaxTotalPct != e.lastAdaptiveParams.MaxTotalPct || params.BuyThreshold != e.lastAdaptiveParams.BuyThreshold) { log.Println(params.LogReason) }
		e.lastAdaptiveParams = params
	}
	if reg, ok := e.alphaEng.(core.StrategyRegistry); ok && e.tickCount%20 == 0 { e.logWeightSnapshot(reg.WeightSnapshot()) }
}

func (e *Engine) processRotation(signals []core.Signal, candidateSignals []core.Signal, stockQuotes map[string]*core.Quote, tradeDay int64) {
	if e.rotationPol == nil || e.posMgr == nil { return }
	ranks := make(map[string]rotation.RankInfo, len(signals))
	for i, sig := range signals { ranks[sig.Symbol] = rotation.RankInfo{Symbol: sig.Symbol, Rank: i + 1, Score: sig.Score} }
	reserved := make(map[string]bool)
	for _, pos := range e.posMgr.AllPositions() {
		q := stockQuotes[pos.Symbol]
		if q == nil { continue }
		rank := ranks[pos.Symbol]
		if rank.Symbol == "" { rank = rotation.RankInfo{Symbol: pos.Symbol, Rank: rotation.MissingRank} }
		pnlPct := (q.Price - pos.AvgPrice) / pos.AvgPrice * 100
		cand := e.bestRotationCandidate(candidateSignals, ranks, pos.Symbol, reserved)
		decision := e.rotationPol.ShouldRotate(pos, rank, cand, time.Now().In(cstZone), pnlPct, tradeDay)
		if !decision.Rotate || pos.SellableQty <= 0 { continue }
		if e.execCtrl != nil && !e.execCtrl.AllowSell(pos.Symbol, "ROTATION") { continue }
		holdTicks := 0
		if e.execCtrl != nil { holdTicks = e.execCtrl.GetHoldTicks(pos.Symbol) }
		log.Print(rotation.FormatSellLog(rank, pnlPct, decision.Candidate, decision.ScoreDelta))
		trade := e.executeOrder(core.Order{Symbol: pos.Symbol, Side: "SELL", Price: q.Bid1, Quantity: pos.SellableQty, Reason: "ROTATION_CONFIRMED"}, stockQuotes)
		if trade != nil {
			if e.execCtrl != nil { e.execCtrl.RecordSell(pos.Symbol, q.Price, "ROTATION") }
			e.rotationPol.RecordRotation(tradeDay)
			reserved[decision.Candidate.Symbol] = true
			if e.perfTracker != nil { e.perfTracker.OnSell(trade, pos.AvgPrice, holdTicks, "ROTATION") }
			if e.safetyGuard != nil { e.safetyGuard.OnTradeClosed(pnlPct) }
			if reg, ok := e.alphaEng.(core.StrategyRegistry); ok { reg.RecordSell(pos.Symbol, pnlPct) }
		}
	}
}

func (e *Engine) bestRotationCandidate(signals []core.Signal, ranks map[string]rotation.RankInfo, oldSymbol string, reserved map[string]bool) *rotation.CandidateInfo {
	for i, sig := range signals {
		if sig.Symbol == oldSymbol || reserved[sig.Symbol] || e.posMgr.HasPosition(sig.Symbol) { continue }
		rank := i + 1
		if info, ok := ranks[sig.Symbol]; ok { rank = info.Rank }
		return &rotation.CandidateInfo{Symbol: sig.Symbol, Rank: rank, Score: sig.Score}
	}
	return nil
}

func (e *Engine) executeOrder(order core.Order, quotes map[string]*core.Quote) *core.Trade {
	if e.executor == nil || e.posMgr == nil || e.tradeLogger == nil { return nil }
	q := quotes[order.Symbol]
	if q == nil { return nil }
	trade, err := e.executor.Execute(&order, q)
	if err != nil { log.Printf("[Engine] order rejected %s %s: %v", order.Side, order.Symbol, err); return nil }
	e.posMgr.ApplyTrade(trade)
	e.tradeLogger.Log(trade)
	return trade
}

func (e *Engine) maybeReview() {
	if e.reviewer == nil { return }
	now := time.Now()
	_, week := now.ISOWeek()
	if now.Weekday() == e.cfg.ReviewWeekday && now.Hour() == e.cfg.ReviewHour && week != e.lastReviewWk {
		e.lastReviewWk = week
		log.Printf("[Engine] triggering weekly review (ISO week %d)", week)
		if err := e.reviewer.Review(); err != nil { log.Printf("[Engine] weekly review error: %v", err) }
	}
}

func (e *Engine) regimeMinScore(signals []core.Signal, state core.MarketState, configuredFloor float64) (float64, string, float64) {
	if len(signals) == 0 { return 0, "no-valid-scores", 0 }
	vals := make([]float64, 0, len(signals))
	for _, sig := range signals {
		if !math.IsNaN(sig.Score) && !math.IsInf(sig.Score, 0) { vals = append(vals, sig.Score) }
	}
	if len(vals) == 0 { return 0, "no-valid-scores", 0 }
	sort.Float64s(vals)
	p90 := percentile(vals, 90)
	switch state {
	case core.MarketOscillate:
		if configuredFloor > p90 { return configuredFloor, "max(p90(total_score),config_floor)", p90 }
		return p90, "p90(total_score)", p90
	case core.MarketUptrend:
		return percentile(vals, 70), "p70(total_score)", p90
	default:
		return 0, "downtrend/no-open", p90
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 { return 0 }
	idx := int(math.Ceil((p/100)*float64(len(sorted)))) - 1
	if idx < 0 { idx = 0 }
	if idx >= len(sorted) { idx = len(sorted) - 1 }
	return sorted[idx]
}

func countSignalsAtLeast(signals []core.Signal, minScore float64) int {
	if minScore <= 0 { return 0 }
	count := 0
	for _, sig := range signals { if sig.Score >= minScore { count++ } }
	return count
}

func (e *Engine) logPortfolioStats(s core.PortfolioStats, positions []core.Position, quotes map[string]*core.Quote) {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("╔══ 资金状况 %s ══════════════════════════════════════════════\n", time.Now().Format("15:04:05")))
	b.WriteString(fmt.Sprintf("║  总资产 ¥%9.0f │ 已用 ¥%9.0f(%4.1f%%) │ 可用 ¥%9.0f │ 可部署 ¥%9.0f\n", s.TotalCapital, s.UsedCapital, s.UsedPct, s.AvailableCapital, s.DeployableCap))
	b.WriteString(fmt.Sprintf("║  持仓 %d/%d  ──────────────────────────────────────────────────\n", s.PositionCount, s.MaxPositions))
	for _, p := range positions {
		cost := p.AvgPrice * float64(p.Quantity)
		pct := 0.0
		if s.TotalCapital != 0 { pct = cost / s.TotalCapital * 100 }
		nowPrice := p.AvgPrice
		if q := quotes[p.Symbol]; q != nil { nowPrice = q.Price }
		unrlzd := 0.0
		if p.AvgPrice != 0 { unrlzd = (nowPrice - p.AvgPrice) / p.AvgPrice * 100 }
		b.WriteString(fmt.Sprintf("║    %-8s qty=%5d  成本=¥%8.0f(%4.1f%%)  现价=%8.4f  浮盈=%+.2f%%\n", p.Symbol, p.Quantity, cost, pct, nowPrice, unrlzd))
	}
	if len(positions) == 0 { b.WriteString("║    (空仓)\n") }
	b.WriteString("╚══════════════════════════════════════════════════════════════")
	log.Println(b.String())
}

func (e *Engine) logRanking(signals []core.Signal, dampened map[string]int) {
	var b strings.Builder
	b.WriteString("┌── Alpha Ranking ────────────────────────────────────────────\n")
	for i, sig := range signals {
		parts := make([]string, 0, len(sig.Breakdown))
		for name, score := range sig.Breakdown { parts = append(parts, fmt.Sprintf("%s=%+.3f", name, score)) }
		sort.Strings(parts)
		dampenTag := ""
		if streak, ok := dampened[sig.Symbol]; ok { dampenTag = fmt.Sprintf("  🔻streak=%d", streak) }
		b.WriteString(fmt.Sprintf("│  #%d  %-8s  total=%+.4f%s  │  %s\n", i+1, sig.Symbol, sig.Score, dampenTag, strings.Join(parts, "  ")))
	}
	b.WriteString("└─────────────────────────────────────────────────────────────")
	log.Println(b.String())
}

func (e *Engine) logStability(counts map[string]int, stableSignals []core.Signal) {
	stableSet := make(map[string]bool, len(stableSignals))
	for _, sig := range stableSignals { stableSet[sig.Symbol] = true }
	syms := make([]string, 0, len(counts))
	for sym := range counts { syms = append(syms, sym) }
	sort.Strings(syms)
	var b strings.Builder
	b.WriteString("── [Stability] ─────────────────────────────────────────────\n")
	for _, sym := range syms {
		cnt := counts[sym]
		status := fmt.Sprintf("⏳ %d tick(s)", cnt)
		if stableSet[sym] { status = fmt.Sprintf("✅ STABLE(%d) → BUY eligible", cnt) }
		b.WriteString(fmt.Sprintf("   %-8s  consecutive=%d  %s\n", sym, cnt, status))
	}
	log.Print(b.String())
}

func (e *Engine) logPnL(quotes map[string]*core.Quote) {
	if e.posMgr == nil { return }
	positions := e.posMgr.AllPositions()
	if len(positions) == 0 { return }
	var b strings.Builder
	b.WriteString("── [持仓盈亏] ──────────────────────────────────────────────\n")
	for _, p := range positions {
		q := quotes[p.Symbol]
		if q == nil { continue }
		unrlzd := 0.0
		maxPnl := 0.0
		if p.AvgPrice != 0 { unrlzd = (q.Price - p.AvgPrice) / p.AvgPrice * 100; maxPnl = (p.HighestPrice - p.AvgPrice) / p.AvgPrice * 100 }
		b.WriteString(fmt.Sprintf("   %-8s  qty=%5d  avg=%8.4f  now=%8.4f  浮盈=%+6.2f%%  峰值=%+6.2f%%\n", p.Symbol, p.Quantity, p.AvgPrice, q.Price, unrlzd, maxPnl))
	}
	log.Print(b.String())
}

func (e *Engine) logWeightSnapshot(weights []core.StrategyWeight) {
	if len(weights) == 0 { return }
	var b strings.Builder
	b.WriteString("── [策略权重快照] ──────────────────────────────────────────\n")
	for _, w := range weights {
		wr := "  n/a"
		if w.TradeCount > 0 { wr = fmt.Sprintf("%5.1f%%", w.WinRate) }
		b.WriteString(fmt.Sprintf("   %-12s  wt=%.3f(base %.3f)  WR=%s  avgPnL=%+.2f%%  trades=%d\n", w.Name, w.Weight, w.BaseWeight, wr, w.AvgPnL, w.TradeCount))
	}
	log.Print(b.String())
}

func abs64(x float64) float64 {
	if x < 0 { return -x }
	return x
}
