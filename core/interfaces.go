// Package core defines the system-wide interface contracts.
// All module boundaries are expressed here; concrete implementations live in
// their own packages and are wired together in main.
package core

import "time"

// ─── 交易日历 ─────────────────────────────────────────────────────────────────

// TradingCalendar determines whether a given date is a trading day and
// provides a monotonically increasing sequence number for each trading day.
//
// Implemented by calendar.Calendar; may be nil in the engine (falls back to
// using tick-count as the trade-day sequence for pure simulation mode).
type TradingCalendar interface {
	// IsTradeDay returns true when t is a regular A-share trading session.
	IsTradeDay(t time.Time) bool

	// TradeDaySeq returns the ordinal trading-day index for t (1-based,
	// counting from a fixed reference date).  Non-trading days return the
	// sequence of the preceding trading day.
	TradeDaySeq(t time.Time) int64

	// IsInTradingHours returns true when t falls within an active A-share
	// continuous-auction session:
	//   Morning:   09:30 – 11:30 (exclusive)  CST
	//   Afternoon: 13:00 – 15:00 (exclusive)  CST
	// Returns false on non-trading days, weekends, and public holidays.
	IsInTradingHours(t time.Time) bool
}

// ─── 行情 ─────────────────────────────────────────────────────────────────────

// DataProvider fetches real-time level-1 snapshots for the requested symbols.
type DataProvider interface {
	GetRealtime(symbols []string) map[string]*Quote
}

// Screener returns the current list of candidate symbols to watch.
type Screener interface {
	Screen() []string
}

// ─── 策略 ─────────────────────────────────────────────────────────────────────

// Strategy evaluates a single quote and emits a binary buy signal.
// Kept for backward-compatibility with simple single-factor wrappers;
// prefer AlphaStrategy for new multi-factor implementations.
type Strategy interface {
	ShouldBuy(q *Quote) bool
}

// ─── 多因子选股 ────────────────────────────────────────────────────────────────

// AlphaStrategy scores a single Quote on a normalised [-1, +1] scale.
//
// Contract (strictly enforced):
//   - +1.0 = maximum bullish signal
//   - -1.0 = maximum bearish signal
//   -  0.0 = neutral / no opinion
//   - Must NOT make buy/sell decisions; that authority belongs to PortfolioDecision alone.
//   - Must NOT mutate any shared state other than its own internal model (e.g. EMA).
type AlphaStrategy interface {
	Name() string
	Score(q *Quote) float64
}

// AlphaEngine aggregates multiple AlphaStrategy scores using configurable
// per-strategy weights and returns a descending-sorted []Signal across all
// symbols present in the quotes map.
type AlphaEngine interface {
	Rank(quotes map[string]*Quote) []Signal
}

// PortfolioDecision is the SOLE authority for generating BUY orders.
//
// SELL responsibility belongs exclusively to PositionManager.CheckExit.
//
// allocations[i] is the exact CNY amount pre-computed by PortfolioManager for
// rank i.  A value of 0 means "no budget for this rank; skip."
type PortfolioDecision interface {
	Decide(
		buySignals  []Signal,
		quotes      map[string]*Quote,
		positions   []Position,
		allocations []float64, // per-rank capital in CNY, len ≥ len(buySignals)
	) []Order
}

// ─── 信号调整 ──────────────────────────────────────────────────────────────────

// SignalAdjuster post-processes a ranked []Signal slice and returns a
// (possibly re-sorted) adjusted slice.
//
// Primary use-case: anti-monopoly dampening.  When a symbol has been ranked
// #1 for too many consecutive ticks its score is multiplied by DampenFactor,
// preventing it from monopolising the portfolio.
//
// Adjust is called once per tick, after AlphaEngine.Rank and before
// SignalStabilizer.Stabilize.
type SignalAdjuster interface {
	Adjust(signals []Signal) (adjusted []Signal, dampenedSymbols map[string]int)
}

// ─── 信号稳定 ──────────────────────────────────────────────────────────────────

// SignalStabilizer enforces entry discipline: a symbol must rank in the
// configured top-N for at least MinConsecutive consecutive ticks before being
// promoted to a "stable" BUY candidate.
//
// Stabilize must be called once per tick with the full ranked signal list.
// It returns:
//   - stableSignals: signals whose consecutive count has reached the threshold
//   - counts: per-symbol current consecutive count (for logging / inspection)
type SignalStabilizer interface {
	Stabilize(signals []Signal) (stableSignals []Signal, counts map[string]int)
}

// ─── 市场趋势过滤 ──────────────────────────────────────────────────────────────

// MarketFilter guards new position openings against adverse macro conditions.
//
// AllowOpen returns true when the broad market trend (e.g. CSI-300 MA20)
// permits new long entries.  When false, the engine suppresses all BUY orders
// from PortfolioDecision while still allowing SELL / risk-exit orders to proceed.
// MarketFilter guards new position openings based on broad-market conditions.
//
// Two-method contract:
//   - AllowOpen – binary gate; false means no new positions this tick.
//   - State     – three-state classification for nuanced signal filtering.
type MarketFilter interface {
	// AllowOpen returns false during confirmed downtrends.
	// Oscillating markets return true; callers should inspect State() to apply
	// additional score filters for choppy conditions.
	AllowOpen(indexQuote *Quote) bool

	// State classifies the current market into one of three regimes:
	//   MarketUptrend   – full allocation allowed
	//   MarketOscillate – open only on strong signals (caller enforces)
	//   MarketDowntrend – AllowOpen always returns false
	State(indexQuote *Quote) MarketState
}

// ─── 持仓 ─────────────────────────────────────────────────────────────────────

// PositionManager owns the in-memory position book and all exit-signal logic.
//
// Design constraint: the ONLY way to mutate the position book is through
// ApplyTrade. AddPosition/RemovePosition are intentionally absent from this
// interface to enforce the invariant: 任何持仓变化必须来自 Trade。
//
// Exit signal convention:
//
//	"HOLD"         – keep position
//	"STOP_LOSS"    – hard floor: loss ≥ StopLossPct
//	"TAKE_PROFIT"  – fixed target: gain ≥ TakeProfitPct
//	"TRAIL_STOP"   – trailing stop fired: gain was ≥ TrailStart, then
//	                 drawdown from HighestPrice ≥ TrailDrop
type PositionManager interface {
	// ApplyTrade is the single entry-point for all position mutations.
	// BUY  → open or merge into an existing position (recalculates AvgPrice).
	// SELL → reduce or close a position.
	ApplyTrade(trade *Trade)

	CheckExit(pos *Position, q *Quote) string
	AllPositions() []Position
	GetPosition(symbol string) (*Position, bool)
	HasPosition(symbol string) bool
	UpdateHighest(symbol string, price float64)

	// AdvanceTradeDay is called by the engine at the start of each tick.
	// currentDay is the current trading-day sequence number (or tick count in
	// simulation mode).  Any position whose BuyTradeDay < currentDay has its
	// SellableQty set to Quantity, implementing the T+1 unlock.
	AdvanceTradeDay(currentDay int64)
}

// ─── 资金 ─────────────────────────────────────────────────────────────────────

// PortfolioManager decides whether and how much capital to deploy.
//
// Lifecycle per tick:
//  1. Call Stats(current)          – log portfolio state
//  2. Call CanOpenPosition(current) – hard gate; abort if false
//  3. Call AllocatePlan(current, n) – get per-rank CNY amounts for up to n ranks
type PortfolioManager interface {
	// CanOpenPosition returns true if the portfolio has room (by count, available
	// cash, and MaxTotalPct cap) to accept at least one more position.
	CanOpenPosition(current []Position) bool

	// AllocatePlan returns a slice of CNY amounts to deploy, one per rank.
	// len(result) == maxRanks; result[i] may be 0 if budget is exhausted.
	// Each amount already respects MaxSinglePct and MaxTotalPct constraints.
	AllocatePlan(current []Position, maxRanks int) []float64

	// Stats returns a snapshot of current portfolio metrics for logging.
	Stats(current []Position) PortfolioStats
}

// ─── 执行节奏控制 ──────────────────────────────────────────────────────────────

// ExecController enforces execution discipline to prevent over-trading,
// high-price re-entry chasing, and premature exits of new positions.
//
// Usage per engine tick (in order):
//  1. AdvanceTick()                    – advance counter, reset per-tick caps
//  2. AllowSell(symbol, exitType)      – Phase 1: check MinHoldTicks / sell cap
//  3. RecordSell(symbol, price, type)  – after confirmed SELL
//  4. AllowBuy(symbol, price)          – Phase 3: check cooldown / high-price
//  5. RecordBuy(symbol, price)         – after confirmed BUY
//
// STOP_LOSS always passes AllowSell (hard stop, never blocked).
type ExecController interface {
	// AdvanceTick must be called at the start of every engine tick.
	// It increments the internal tick counter and resets per-tick buy/sell caps.
	AdvanceTick()

	// AllowBuy returns (true, "") if buying symbol at price is permitted.
	// Returns (false, reason) when blocked; reason is one of:
	//   "COOLDOWN(n/N ticks, last_exit=…)"   – within post-sell cooldown window
	//   "HIGH_PRICE_REENTRY(…)"              – current price > last-sell price
	//   "MAX_BUY_LIMIT(n/N)"                 – per-tick BUY cap reached
	AllowBuy(symbol string, price float64) (bool, string)

	// AllowSell returns true if a SELL with the given exitType is permitted.
	//   - "STOP_LOSS" always returns true (hard stop, bypasses all guards).
	//   - Other exit types are subject to MinHoldTicks and per-tick sell cap.
	AllowSell(symbol string, exitType string) bool

	// RecordBuy must be called immediately after a BUY trade is confirmed.
	RecordBuy(symbol string, price float64)

	// RecordSell must be called immediately after a SELL trade is confirmed.
	RecordSell(symbol string, price float64, exitType string)

	// GetHoldTicks returns the number of ticks the current open position in
	// symbol has been held. Returns 0 if no buy record exists (unknown entry).
	// Must be called BEFORE RecordSell for the same symbol.
	GetHoldTicks(symbol string) int
}

// ─── 策略评估 ──────────────────────────────────────────────────────────────────

// PerformanceTracker records closed trades, maintains an equity curve, and
// computes strategy evaluation metrics (win-rate, profit factor, drawdown…).
//
// Usage per engine tick:
//  1. OnBuy(trade)                           – after confirmed BUY
//  2. OnSell(trade, entryAvgPrice, holdTicks, exitType) – after confirmed SELL
//  3. RecordEquity(equity)                   – end of tick (cash + market value)
//  4. MaybeReport(tick)                      – optional periodic output
type PerformanceTracker interface {
	// OnBuy deducts the trade cost from the tracked cash balance.
	OnBuy(trade *Trade)

	// OnSell credits the trade proceeds, records a ClosedTrade, and updates
	// aggregate metrics.
	// entryAvgPrice is the position's weighted average cost (from PositionManager).
	// holdTicks is from ExecController (0 if unavailable).
	// exitType is one of "STOP_LOSS" | "TAKE_PROFIT" | "TRAIL_STOP".
	OnSell(trade *Trade, entryAvgPrice float64, holdTicks int, exitType string)

	// RecordEquity appends the current total equity to the historical curve.
	// equity = Cash() + Σ(position.Qty × currentMarketPrice)
	RecordEquity(equity float64)

	// MaybeReport prints a formatted summary report if enough ticks have
	// elapsed since the last report. No-op otherwise.
	MaybeReport(tick int)

	// Report computes and returns the latest performance metrics.
	Report() PerformanceReport

	// Cash returns the current tracked cash balance.
	Cash() float64

	// ClosedTrades returns a snapshot copy of all recorded closed trades,
	// newest last. Safe to call at any time.
	ClosedTrades() []ClosedTrade
}

// ─── 执行 ─────────────────────────────────────────────────────────────────────

// Executor translates an Order into an executed Trade.
//
// Quote is provided so the executor can:
//   - verify the order price is within circuit-breaker limits (±10 % of PrevClose)
//   - apply realistic slippage relative to the live spread
//   - simulate partial fills based on current volume
//
// The returned Trade is the single source of truth for position mutations.
type Executor interface {
	Execute(order *Order, quote *Quote) (*Trade, error)
}

// ─── 记录 ─────────────────────────────────────────────────────────────────────

// TradeLogger persists every executed trade for audit and review.
type TradeLogger interface {
	Log(trade *Trade)
}

// ─── 复盘 ─────────────────────────────────────────────────────────────────────

// Reviewer produces a periodic performance report from the trade log.
type Reviewer interface {
	Review() error
}

// ─── 多策略自适应扩展 ──────────────────────────────────────────────────────────

// StrategyRegistry is an optional extension of AlphaEngine that supports
// per-strategy performance attribution and dynamic weight adjustment.
//
// The engine detects this interface via a type assertion on alphaEng and calls
// RecordBuy / RecordSell automatically — no wiring change required in main.
type StrategyRegistry interface {
	AlphaEngine

	// RecordBuy stores which strategy dominated the BUY signal for attribution.
	// breakdown is Signal.Breakdown from the signal that triggered the order.
	RecordBuy(symbol string, breakdown map[string]float64)

	// RecordSell credits or debits the closed-trade PnL to the entry strategy.
	// pnlPct = (exitPrice − entryAvgPrice) / entryAvgPrice × 100.
	RecordSell(symbol string, pnlPct float64)

	// WeightSnapshot returns a snapshot of all strategies' current weight and
	// attributed performance statistics for logging.
	WeightSnapshot() []StrategyWeight
}

// AdaptiveOptimizer derives recommended runtime trading parameters from the
// current PerformanceReport.  The engine calls Params every tick and applies
// the result via MaxTotalPctSetter / BuyThresholdSetter if implemented.
type AdaptiveOptimizer interface {
	Params(report PerformanceReport) AdaptiveParams
}

// MaxTotalPctSetter is an optional interface for runtime position-size adjustment.
// Implemented by portfolio.Manager.
type MaxTotalPctSetter interface {
	SetMaxTotalPct(pct float64)
}

// BuyThresholdSetter is an optional interface for runtime entry-threshold adjustment.
// Implemented by topn.Decision.
type BuyThresholdSetter interface {
	SetBuyThreshold(threshold float64)
}

// ─── Paper Trading interfaces ─────────────────────────────────────────────────

// Broker abstracts the connection to an order-execution venue.
//
// In paper trading mode the implementation wraps a local Executor.
// In live trading mode it connects to a real broker API (e.g. XTP, CTP).
//
// The interface is intentionally minimal: position state is authoritative in
// PositionManager; QueryPosition is provided for reconciliation only.
type Broker interface {
	// PlaceOrder submits an order and returns a unique order ID.
	PlaceOrder(order *Order) (orderID string, err error)

	// CancelOrder requests cancellation of a pending (unfilled) order.
	// Returns an error if the order is not found or already filled.
	CancelOrder(orderID string) error

	// QueryPosition returns the broker-side position for a symbol.
	// Used to reconcile local state against broker state on startup/reconnect.
	QueryPosition(symbol string) (*Position, bool)

	// QueryAccount returns the broker-reported cash balance and total equity.
	QueryAccount() (cash float64, equity float64)
}

// Monitor tracks portfolio health and emits risk-level alerts in real time.
//
// The engine calls Update every tick at the end of Phase 5 (after equity is
// computed).  The monitor classifies the current drawdown into a RiskLevel
// and fires registered alert callbacks when the level escalates.
type Monitor interface {
	// Update feeds the latest equity, performance report, and position snapshot.
	// Called once per engine tick; safe for concurrent use.
	Update(equity float64, report PerformanceReport, positions []Position)

	// State returns the most recent MonitorState snapshot.
	State() MonitorState
}

// DashboardReporter receives the full market context each engine tick and
// publishes it to connected dashboard clients.
//
// Called by the engine at the end of Phase 5, after equity is computed, so
// all per-tick data (quotes, positions, performance report) are consistent.
//
// Implementing this interface is optional; when nil, the engine skips it.
type DashboardReporter interface {
	// OnTick is called once per engine tick.
	//   equity    – cash + Σ(position.Qty × currentPrice)
	//   report    – latest PerformanceReport from PerformanceTracker.Report()
	//   positions – snapshot of all open positions at end of tick
	//   quotes    – all stock quotes fetched this tick (may be nil/empty)
	OnTick(equity float64, report PerformanceReport, positions []Position, quotes map[string]*Quote)

	// OnQuoteRefresh is only used outside trading hours to refresh dashboard quote display.
	// It does not represent a trading tick, and must not advance equity-curve tick history
	// or produce persistent side effects.
	OnQuoteRefresh(equity float64, report PerformanceReport, positions []Position, quotes map[string]*Quote)
}

// DashboardMarketReporter is an optional display-only interface for syncing
// already-computed market state to the dashboard.
//
// The engine calls SetMarketState after market state calculation is complete,
// passing MarketState.String() and the current index price to the dashboard.
// Implementations must not use this display synchronization hook to influence
// trading decisions.
type DashboardMarketReporter interface {
	SetMarketState(state string, indexPrice float64)
}

// ─── 最终安全控制层 ────────────────────────────────────────────────────────────

// SafetyGuard is the final safety control layer for pre-live deployment.
//
// It operates above all other risk layers and enforces:
//  1. Losing-streak position suppression (streak ≥ N → reduce or freeze)
//  2. Manual operator controls (stop_opening / force_liquidate)
//  3. Execution anomaly detection (latency spikes / abnormal fill rates)
//
// Usage per engine tick:
//  1. AdvanceTick()               – at tick start (alongside execCtrl.AdvanceTick)
//  2. ShouldForceLiquidate()      – Phase 1 start: if true, liquidate all positions
//  3. AcknowledgeForceLiquidate() – after liquidation is complete
//  4. OnTradeClosed(pnlPct)       – after each confirmed SELL
//  5. AllowOpen()                 – Phase 3 gate before any BUY decision
//  6. CheckExecution(rec)         – after each execution record (broker callback)
type SafetyGuard interface {
	// AdvanceTick must be called once per engine tick.
	// Decrements the streak-based freeze countdown and updates MaxTotalPct.
	AdvanceTick()

	// OnTradeClosed updates the consecutive losing-streak counter.
	// pnlPct is the closed trade's PnL as a percentage (negative = loss).
	// Must be called after every confirmed SELL.
	OnTradeClosed(pnlPct float64)

	// AllowOpen returns true when new BUY orders are permitted.
	// Returns false when: streak freeze active, manual stop in effect, or
	// trading has been halted due to execution anomalies.
	AllowOpen() bool

	// ShouldForceLiquidate returns true when TriggerForceLiquidate has been
	// called and the engine has not yet acknowledged it.
	// Returns true for at most one tick per trigger call.
	ShouldForceLiquidate() bool

	// AcknowledgeForceLiquidate clears the force-liquidate flag.
	// Must be called by the engine after all positions have been closed.
	AcknowledgeForceLiquidate()

	// ── Operator controls ─────────────────────────────────────────────────

	// StopOpening prevents new position openings. Does not affect exit orders.
	StopOpening()

	// ResumeOpening re-enables position openings (undoes StopOpening).
	ResumeOpening()

	// TriggerForceLiquidate signals the engine to close all open positions
	// immediately on the next tick. Safe to call from any goroutine.
	TriggerForceLiquidate()

	// ── Anomaly detection ─────────────────────────────────────────────────

	// CheckExecution analyses an ExecutionRecord for anomalies (high latency
	// or very low fill rate). When the anomaly count within the rolling window
	// exceeds the configured threshold, all trading is halted automatically.
	CheckExecution(rec *ExecutionRecord)

	// ── Inspection ────────────────────────────────────────────────────────

	// SafetyStatus returns a point-in-time snapshot of the guard state.
	SafetyStatus() SafetyStatus
}

