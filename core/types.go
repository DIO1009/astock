// Package core defines all shared domain types.
// Every other package depends on this package; nothing here depends on
// anything else in the project.
package core

// Quote represents a real-time level-1 market snapshot.
//
// Derived fields (Return5d, Return20d, EMA20, Volatility) MUST be populated by
// the DataProvider, not by individual strategies.  This is the single source of
// truth for all multi-period metrics; strategies are forbidden from maintaining
// their own price history.
type Quote struct {
	Symbol string
	Price  float64

	// ── Level-1 fields ──────────────────────────────────────────────────────
	PrevClose float64 // 昨收价
	Bid1      float64
	Ask1      float64
	Volume    int64

	// ── Single-period change ─────────────────────────────────────────────────
	PctChg float64 // (Price−PrevClose)/PrevClose×100

	// ── Multi-period returns (populated by DataProvider) ─────────────────────
	Return5d  float64 // (Price−Price5TicksAgo)/Price5TicksAgo×100; 0 if history insufficient
	Return20d float64 // (Price−Price20TicksAgo)/Price20TicksAgo×100; 0 if history insufficient

	// ── Technical indicators (populated by DataProvider) ─────────────────────
	EMA20       float64 // 20-period Exponential Moving Average
	Volatility  float64 // rolling std-dev of per-tick returns (%), e.g. last 20 ticks
	AvgVolume5d float64 // rolling average volume over the last 5 ticks
	VolumeRatio float64 // Volume / AvgVolume5d; 0 when AvgVolume5d unavailable

	Timestamp int64
}

// Position is a currently open holding.
// AvgPrice is the authoritative cost basis; EntryPrice records only the
// price of the very first fill and is kept for audit purposes.
//
// T+1 enforcement: SellableQty tracks how much can be sold today.
// On the day of purchase, SellableQty = 0.  PositionManager.AdvanceTradeDay
// sets SellableQty = Quantity at the start of every new trading day.
type Position struct {
	Symbol       string
	EntryPrice   float64 // 首次建仓成交价（只写一次，不随加仓变动）
	AvgPrice     float64 // 加权平均成本 = Σ(fillPrice×qty) / totalQty
	HighestPrice float64 // 建仓后的最高成交价水位（用于移动止损）
	Quantity     int     // 总持仓数量（含当日买入的锁定部分）
	SellableQty  int     // T+1 可卖数量：当日买入=0，次交易日起=Quantity
	BuyTradeDay  int64   // 最后一次买入时的交易日序号（由引擎赋值）
}

// Order is an instruction to buy or sell.
// Reason is a human-readable string describing why the order was generated
// (e.g. "ALPHA rank#1 score=+0.32 stable=4" or "STOP_LOSS loss=-5.2%").
// It is propagated to the Trade for logging and audit.
type Order struct {
	Symbol   string
	Side     string // "BUY" | "SELL"
	Price    float64
	Quantity int
	Reason   string
}

// Trade is the record of an executed fill.
// Reason is carried from the originating Order unchanged.
type Trade struct {
	Symbol    string
	Side      string
	Price     float64
	Quantity  int
	Reason    string
	Timestamp int64
}

// PortfolioStats is a per-tick snapshot of portfolio-level metrics.
// Returned by PortfolioManager.Stats for logging and risk checks.
type PortfolioStats struct {
	TotalCapital     float64 // configured account size (CNY)
	UsedCapital      float64 // Σ(AvgPrice × Qty) for all open positions
	AvailableCapital float64 // TotalCapital − UsedCapital  (raw cash on hand)
	DeployableCap    float64 // max(0, TotalCapital×MaxTotalPct − UsedCapital)
	UsedPct          float64 // UsedCapital / TotalCapital × 100 (%)
	PositionCount    int
	MaxPositions     int
}

// MarketState is the three-state market classification used by MarketFilter.
//
//	MarketUptrend   – price substantially above MA, bullish momentum
//	MarketOscillate – price near MA, no clear directional bias
//	MarketDowntrend – price substantially below MA, bearish momentum
type MarketState uint8

const (
	MarketUptrend   MarketState = iota
	MarketOscillate             // ranging / choppy
	MarketDowntrend
)

func (s MarketState) String() string {
	switch s {
	case MarketUptrend:
		return "UPTREND"
	case MarketOscillate:
		return "OSCILLATE"
	case MarketDowntrend:
		return "DOWNTREND"
	default:
		return "UNKNOWN"
	}
}

// ClosedTrade captures the complete lifecycle of one position (entry → exit).
// It is recorded by PerformanceTracker after every confirmed SELL execution.
type ClosedTrade struct {
	Symbol     string
	EntryPrice float64 // weighted average cost (AvgPrice at time of sell)
	ExitPrice  float64 // actual fill price
	Quantity   int
	PnlPct     float64 // (ExitPrice−EntryPrice)/EntryPrice×100
	HoldTicks  int     // number of ticks position was held
	ExitReason string  // "STOP_LOSS" | "TAKE_PROFIT" | "TRAIL_STOP"
	Timestamp  int64   // Unix milliseconds at exit
}

// PerformanceReport is the output of PerformanceTracker.Report().
// All percentage fields are expressed as absolute percentage points
// (e.g. 12.5 means 12.5 %, not 0.125).
type PerformanceReport struct {
	TickCount      int
	InitialCapital float64
	CurrentEquity  float64
	TotalReturn    float64 // (CurrentEquity−InitialCapital)/InitialCapital×100
	MaxDrawdown    float64 // peak-to-trough drawdown from equity curve (positive %)
	WinRate        float64 // WinCount/TradeCount×100
	AvgWin         float64 // avg pnl% across winning trades
	AvgLoss        float64 // avg |pnl%| across losing trades (positive number)
	ProfitFactor   float64 // Σ wins / Σ |losses|; 0 if no losses
	TradeCount     int
	WinCount       int
	LossCount      int
	AvgHoldTicks   float64

	// Breakdown by exit reason
	StopLossCount   int
	TakeProfitCount int
	TrailStopCount  int

	// ─── Feature 5: Enhanced statistics ──────────────────────────────────────

	// MaxConsecutiveLoss is the length of the longest consecutive losing streak
	// (number of back-to-back losing trades).
	MaxConsecutiveLoss int

	// MaxConsecutiveLossPct is the cumulative PnL% loss during that worst streak.
	// Expressed as a negative number (e.g. -15.3 means the streak lost 15.3%).
	MaxConsecutiveLossPct float64

	// Top5PnlConcentration is the fraction of total gross winning PnL that comes
	// from the top-5 (largest) winning trades, expressed as a percentage.
	// High values (>60%) indicate earnings are driven by a few outlier wins.
	Top5PnlConcentration float64

	// SharpeProxy is a simplified annualised Sharpe-like ratio from the equity curve:
	//   mean_tick_return / std_tick_return × sqrt(252)
	// Approximation only — no risk-free rate deduction.
	SharpeProxy float64
}

// StrategyWeight is a snapshot of one strategy's current weight and attributed performance.
// Returned by StrategyRegistry.WeightSnapshot for logging and review.
type StrategyWeight struct {
	Name       string
	Weight     float64 // current dynamic weight (raw; Rank normalises by ΣWeight)
	BaseWeight float64 // configured baseline weight
	WinRate    float64 // % of attributed closed trades that were wins (0 if none yet)
	AvgPnL     float64 // average PnL% for attributed closed trades
	TradeCount int     // number of attributed closed trades
}

// AdaptiveParams contains runtime-adjusted trading parameters returned by
// AdaptiveOptimizer.Params.
type AdaptiveParams struct {
	MaxTotalPct  float64 // recommended capital-deployment ratio [0,1]
	BuyThreshold float64 // recommended minimum alpha score to trigger BUY
	LogReason    string  // non-empty when a rule fired; used for engine logging
}

// Score is the weighted aggregate across all AlphaStrategy scores, normalised
// to [-1, +1].  Positive → bullish, negative → bearish, zero → neutral.
// Breakdown carries each strategy's raw score for logging and review.
type Signal struct {
	Symbol    string
	Score     float64            // weighted aggregate [-1, +1]
	Breakdown map[string]float64 // strategyName → clamped raw score
	Timestamp int64
}

// FactorDiagnosticInput captures same-tick raw inputs for factor diagnostics.
// All fields must come from the live/replay provider, never synthetic fallback values.
type FactorDiagnosticInput struct {
	Symbol        string
	Close         float64
	Close1dAgo    float64
	Close5dAgo    float64
	Close20dAgo   float64
	VolumeToday   int64
	AvgVolume5d   float64
	PctChg        float64
	Return5dRaw   float64
	Return20dRaw  float64
	EMA20         float64
	VolatilityRaw float64
	VolumeRatio   float64
}

// ─── Paper Trading types ──────────────────────────────────────────────────────

// RiskLevel represents the current portfolio risk state.
// Levels escalate monotonically; EMERGENCY is the Kill Switch threshold.
type RiskLevel int

const (
	RiskNormal    RiskLevel = iota // 正常运行，无限制
	RiskCaution                    // 注意：回撤 > 3%
	RiskDefense                    // 防御：回撤 > 5%，AdaptiveOptimizer 缩仓
	RiskEmergency                  // 紧急：回撤 > 8%，Kill Switch 触发
)

func (r RiskLevel) String() string {
	switch r {
	case RiskNormal:
		return "NORMAL"
	case RiskCaution:
		return "CAUTION"
	case RiskDefense:
		return "DEFENSE"
	case RiskEmergency:
		return "EMERGENCY"
	default:
		return "UNKNOWN"
	}
}

// ExecutionRecord captures the full lifecycle of one order execution attempt.
// Recorded by the Paper Broker for every PlaceOrder call, whether filled or rejected.
// Used downstream by DeviationAnalyzer to compute slippage statistics.
type ExecutionRecord struct {
	OrderID string // unique order identifier (e.g. "ORD-42")
	Symbol  string
	Side    string // "BUY" | "SELL"

	// Price deviation
	TheoreticalPrice float64 // order price at signal time (Quote.Price or Bid1/Ask1)
	ActualPrice      float64 // fill price after slippage + commission; 0 if rejected
	SlippagePct      float64 // (ActualPrice−TheoreticalPrice)/TheoreticalPrice×100

	// Quantity
	OrderQty  int
	FilledQty int
	FillRate  float64 // FilledQty/OrderQty×100

	// Status
	Status string // "FILLED" | "PARTIAL" | "REJECTED"

	// Timing
	SignalTime    int64 // Unix ms – quote.Timestamp (when Alpha signal used this quote)
	OrderTime     int64 // Unix ms – when the Order was submitted to broker
	ExecutionTime int64 // Unix ms – when the fill was confirmed
	Latency       int64 // ExecutionTime − SignalTime (ms)

	Reason string // from Order.Reason (exit type, alpha score, etc.)
}

// AlertEvent is emitted by the Monitor when a risk threshold is crossed.
type AlertEvent struct {
	Level     RiskLevel
	Message   string
	Timestamp int64
	Equity    float64
	Drawdown  float64 // current drawdown from peak (%)
}

// MonitorState is a point-in-time snapshot of the portfolio health dashboard.
// Returned by Monitor.State() for logging and external inspection.
type MonitorState struct {
	Timestamp   int64
	Equity      float64
	PeakEquity  float64
	DrawdownPct float64   // current peak-to-trough drawdown (%)
	RiskLevel   RiskLevel // current risk classification
	Positions   []Position
	TradeCount  int
	WinRate     float64 // from PerformanceReport
}

// SafetyStatus is a point-in-time snapshot of the SafetyGuard state.
// Returned by SafetyGuard.SafetyStatus() for logging and external inspection.
type SafetyStatus struct {
	// CurrentStreak is the number of consecutive losing trades so far.
	CurrentStreak int

	// FreezeTicksLeft is the number of ticks remaining in the streak-triggered
	// new-open freeze period. 0 means the guard is not currently frozen.
	FreezeTicksLeft int

	// StreakScale is the current MaxTotalPct multiplier applied due to losing
	// streak. 1.0 = normal, 0.5 = halved (streak ≥ StreakHalfPositionAt).
	StreakScale float64

	// StreakHalfPositionAt is the losing-streak threshold at which StreakScale
	// starts reducing position size. Dashboard/external inspection only.
	StreakHalfPositionAt int

	// StreakFreezeAt is the losing-streak threshold at which new opens are
	// frozen. Dashboard/external inspection only.
	StreakFreezeAt int

	// StreakPositionScale is the position-size multiplier applied when the
	// half-position threshold is reached. Dashboard/external inspection only.
	StreakPositionScale float64

	// ManualStopOpen is true when an operator has called StopOpening().
	ManualStopOpen bool

	// ForceLiqPending is true when TriggerForceLiquidate() has been called
	// and the engine has not yet acknowledged the liquidation.
	ForceLiqPending bool

	// AbnormalCount is the number of abnormal executions detected in the
	// current rolling window (latency spikes or very low fill rates).
	AbnormalCount int

	// TradingStopped is true when the guard has halted all trading due to
	// repeated execution anomalies.
	TradingStopped bool
}
