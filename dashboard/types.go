// Package dashboard implements the real-time Trading Cockpit:
// a WebSocket server that pushes per-tick snapshots to connected browsers
// and accepts operator control commands (stop_opening / force_liquidate / …).
//
// JSON protocol:
//
//	Server → Client: Snapshot (every tick)
//	Client → Server: Command  (operator action)
package dashboard

// ─── Outbound (server → client) ──────────────────────────────────────────────

// Snapshot is the full dashboard payload broadcast to all WebSocket clients
// once per engine tick.  All monetary values are in CNY.
type Snapshot struct {
	Timestamp int64 `json:"ts"` // Unix milliseconds

	Account         AccountInfo       `json:"account"`
	Equity          []EquityPoint     `json:"equity"` // history for chart
	Positions       []PositionInfo    `json:"positions"`
	PositionHistory []ClosedTradeInfo `json:"position_history"` // all-time closed trades
	Trades          []TradeInfo       `json:"trades"`           // last 50 order executions
	Safety          SafetyInfo        `json:"safety"`
	Risk            RiskInfo          `json:"risk"` // Portfolio Risk Engine
	Market          MarketInfo        `json:"market"`
	Strategies      []StrategyInfo    `json:"strategies"`
	Execution       ExecInfo          `json:"execution"`  // aggregate slippage stats
	Alerts          []AlertInfo       `json:"alerts"`     // last 30 alerts
	Candidates      []CandidateInfo   `json:"candidates"` // today's Top-20 candidate pool
}

// AccountInfo – Module 1: Account Overview.
type AccountInfo struct {
	TotalEquity        float64 `json:"total_equity"`
	InitialCapital     float64 `json:"initial_capital"`
	Cash               float64 `json:"cash"`
	InvestedValue      float64 `json:"invested_value"`
	TodayReturnPct     float64 `json:"today_return_pct"`
	TotalReturnPct     float64 `json:"total_return_pct"`
	CurrentDrawdownPct float64 `json:"current_drawdown_pct"`
	MaxDrawdownPct     float64 `json:"max_drawdown_pct"`
	PositionPct        float64 `json:"position_pct"` // invested / total (%)
	RiskLevel          string  `json:"risk_level"`   // NORMAL / CAUTION / DEFENSE / EMERGENCY
	TickCount          int     `json:"tick_count"`
	WinRate            float64 `json:"win_rate"`
	TradeCount         int     `json:"trade_count"`
	ProfitFactor       float64 `json:"profit_factor"`
}

// EquityPoint – one data point on the equity / drawdown chart.
type EquityPoint struct {
	Tick     int     `json:"tick"`
	Equity   float64 `json:"equity"`
	Drawdown float64 `json:"drawdown"` // peak-to-trough % at this tick
}

// PositionInfo – Module 3: one open position row.
type PositionInfo struct {
	Symbol       string  `json:"symbol"`
	Quantity     int     `json:"quantity"`
	AvgPrice     float64 `json:"avg_price"`
	CurrentPrice float64 `json:"current_price"`
	Cost         float64 `json:"cost"`         // AvgPrice × Qty
	MarketValue  float64 `json:"market_value"` // CurrentPrice × Qty
	PnlPct       float64 `json:"pnl_pct"`
	PnlAbs       float64 `json:"pnl_abs"`
	DefenseFlag  bool    `json:"defense_flag"` // true when risk tier ≥ DEFENSE
}

// TradeInfo – Module 4: one execution record row.
type TradeInfo struct {
	OrderID     string  `json:"order_id"`
	Symbol      string  `json:"symbol"`
	Side        string  `json:"side"` // BUY / SELL
	TheoPrice   float64 `json:"theo_price"`
	FillPrice   float64 `json:"fill_price"`
	SlippagePct float64 `json:"slippage_pct"`
	Qty         int     `json:"qty"`
	FilledQty   int     `json:"filled_qty"`
	FillRate    float64 `json:"fill_rate"` // %
	Status      string  `json:"status"`    // FILLED / PARTIAL / REJECTED
	LatencyMs   int64   `json:"latency_ms"`
	Timestamp   int64   `json:"timestamp"` // Unix ms
	Reason      string  `json:"reason"`
}

// SafetyInfo – Module 5: SafetyGuard state.
type SafetyInfo struct {
	Streak          int     `json:"streak"`
	FreezeLeft      int     `json:"freeze_left"`
	StreakScale     float64 `json:"streak_scale"`
	ManualStopOpen  bool    `json:"manual_stop_open"`
	ForceLiqPending bool    `json:"force_liq_pending"`
	AbnormalCount   int     `json:"abnormal_count"`
	TradingStopped  bool    `json:"trading_stopped"`
	AllowOpen       bool    `json:"allow_open"`
}

// RiskInfo – Portfolio Risk Engine state (from risk.Engine).
type RiskInfo struct {
	Tier         string  `json:"tier"` // NORMAL/CAUTION/REDUCED/DEFENSE/FROZEN
	DrawdownPct  float64 `json:"drawdown_pct"`
	VolPct       float64 `json:"vol_pct"`
	DDScale      float64 `json:"dd_scale"`
	VolScale     float64 `json:"vol_scale"`
	EffectivePct float64 `json:"effective_pct"` // current MaxTotalPct (0–1)
	IsFrozen     bool    `json:"is_frozen"`
	FreezeLeft   int     `json:"freeze_left"`
}

// MarketInfo – Module 6: market regime.
type MarketInfo struct {
	State      string  `json:"state"`       // UPTREND / DOWNTREND / OSCILLATE
	IndexPrice float64 `json:"index_price"` // CSI-300 last price
}

// StrategyInfo – one row in the strategy weight table.
type StrategyInfo struct {
	Name       string  `json:"name"`
	Weight     float64 `json:"weight"`
	BaseWeight float64 `json:"base_weight"`
	WinRate    float64 `json:"win_rate"`
	AvgPnl     float64 `json:"avg_pnl"`
	Trades     int     `json:"trades"`
}

// ExecInfo – Module 7: aggregate execution quality stats.
type ExecInfo struct {
	TotalOrders    int     `json:"total_orders"`
	FillRate       float64 `json:"fill_rate"`      // fully filled / total (%)
	RejectionRate  float64 `json:"rejection_rate"` // rejected / total (%)
	AvgSlippagePct float64 `json:"avg_slippage_pct"`
	P50SlippagePct float64 `json:"p50_slippage_pct"`
	P90SlippagePct float64 `json:"p90_slippage_pct"`
	AvgLatencyMs   float64 `json:"avg_latency_ms"`
	P90LatencyMs   float64 `json:"p90_latency_ms"`
}

// AlertInfo – Module 8: one alert entry.
type AlertInfo struct {
	Level     string  `json:"level"` // CAUTION / DEFENSE / EMERGENCY / ANOMALY
	Message   string  `json:"message"`
	Timestamp int64   `json:"timestamp"` // Unix ms
	Drawdown  float64 `json:"drawdown"`
}

// CandidateInfo – one row in today's candidate pool (Top-20 from alpha_rankings).
// Price and PctChg are populated from live quotes when available.
// LiveScore and Breakdown are updated every engine tick from the real-time alpha engine.
type CandidateInfo struct {
	Rank        int                `json:"rank"`
	Symbol      string             `json:"symbol"`
	Name        string             `json:"name"`
	Score       float64            `json:"score"`      // daily alpha score (from DB, set once per day)
	LiveScore   float64            `json:"live_score"` // real-time tick score from engine (updated every tick)
	Breakdown   map[string]float64 `json:"breakdown"`  // per-strategy scores for this tick
	Price       float64            `json:"price"`      // live price (falls back to scoring-day price)
	PctChg      float64            `json:"pct_chg"`    // today's % change from live quote
	Ret5d       float64            `json:"ret5d"`      // 5-day return at scoring time
	VolumeRatio float64            `json:"vol_ratio"`  // volume ratio at scoring time
	MktCapB     float64            `json:"mkt_cap_b"`  // market cap in 亿 CNY
	Stability   int                `json:"stability"`  // consecutive ticks this symbol ranked in Top-N
	InPosition  bool               `json:"in_pos"`     // whether the symbol is currently held
}

// ClosedTradeInfo – one row in the position history (a fully closed position).
type ClosedTradeInfo struct {
	Symbol     string  `json:"symbol"`
	EntryPrice float64 `json:"entry_price"` // weighted average cost at time of exit
	ExitPrice  float64 `json:"exit_price"`  // actual fill price
	Quantity   int     `json:"quantity"`
	PnlPct     float64 `json:"pnl_pct"`     // (ExitPrice−EntryPrice)/EntryPrice×100
	PnlAbs     float64 `json:"pnl_abs"`     // realised PnL value in CNY
	HoldTicks  int     `json:"hold_ticks"`  // ticks held
	ExitReason string  `json:"exit_reason"` // 中文原因：止损 / 止盈 / 追踪止盈 / 轮动调仓 / 卖出
	Timestamp  int64   `json:"timestamp"`   // Unix ms at exit
}

// ─── Inbound (client → server) ────────────────────────────────────────────────

// Command is an operator control instruction sent from the browser to the server.
type Command struct {
	Type   string `json:"type"`   // always "command"
	Action string `json:"action"` // "stop_opening" | "resume_opening" | "force_liquidate"
}
