// Auto-generated from dashboard/types.go – keep in sync.

export interface Snapshot {
  ts: number
  account: AccountInfo
  equity: EquityPoint[]
  positions: PositionInfo[]
  position_history: ClosedTradeInfo[]
  trades: TradeInfo[]
  safety: SafetyInfo
  risk: RiskInfo
  market: MarketInfo
  strategies: StrategyInfo[]
  execution: ExecInfo
  alerts: AlertInfo[]
  candidates: CandidateInfo[]
}

export interface AccountInfo {
  total_equity: number
  initial_capital: number
  cash: number
  invested_value: number
  today_return_pct: number
  total_return_pct: number
  current_drawdown_pct: number
  max_drawdown_pct: number
  position_pct: number
  risk_level: string   // NORMAL | CAUTION | DEFENSE | EMERGENCY
  tick_count: number
  win_rate: number
  trade_count: number
  profit_factor: number
}

export interface EquityPoint {
  tick: number
  equity: number
  drawdown: number
}

export interface PositionInfo {
  symbol: string
  quantity: number
  avg_price: number
  current_price: number
  cost: number
  market_value: number
  pnl_pct: number
  pnl_abs: number
  defense_flag: boolean
}

export interface TradeInfo {
  order_id: string
  symbol: string
  side: string         // BUY | SELL
  theo_price: number
  fill_price: number
  slippage_pct: number
  qty: number
  filled_qty: number
  fill_rate: number
  status: string       // FILLED | PARTIAL | REJECTED
  latency_ms: number
  timestamp: number    // Unix ms
  reason: string
}

export interface SafetyInfo {
  streak: number
  freeze_left: number
  streak_scale: number
  streak_half_position_at: number
  streak_freeze_at: number
  streak_position_scale: number
  manual_stop_open: boolean
  force_liq_pending: boolean
  abnormal_count: number
  trading_stopped: boolean
  allow_open: boolean
}

export interface RiskInfo {
  tier: string         // NORMAL | CAUTION | REDUCED | DEFENSE | FROZEN
  drawdown_pct: number
  vol_pct: number
  dd_scale: number
  vol_scale: number
  effective_pct: number
  is_frozen: boolean
  freeze_left: number
}

export interface MarketInfo {
  state: string        // UPTREND | DOWNTREND | OSCILLATE
  index_price: number
}

export interface StrategyInfo {
  name: string
  weight: number
  base_weight: number
  win_rate: number
  avg_pnl: number
  trades: number
}

export interface ExecInfo {
  total_orders: number
  fill_rate: number
  rejection_rate: number
  avg_slippage_pct: number
  p50_slippage_pct: number
  p90_slippage_pct: number
  avg_latency_ms: number
  p90_latency_ms: number
}

export interface AlertInfo {
  level: string        // CAUTION | DEFENSE | EMERGENCY | ANOMALY
  message: string
  timestamp: number
  drawdown: number
}

export type CommandAction = 'stop_opening' | 'resume_opening' | 'force_liquidate'

export interface ClosedTradeInfo {
  symbol: string
  entry_price: number
  exit_price: number
  quantity: number
  pnl_pct: number
  pnl_abs: number
  hold_ticks: number
  exit_reason: string
  timestamp: number    // Unix ms
}

export interface CandidateInfo {
  rank: number
  symbol: string
  name: string
  score: number        // daily alpha score from DB (set once per day)
  live_score: number   // real-time tick score from engine (updated every tick)
  breakdown: Record<string, number> | null  // per-strategy scores for this tick
  price: number        // live price, falls back to scoring-day price
  pct_chg: number      // today's % change from live quote
  ret5d: number        // 5-day return at scoring time
  vol_ratio: number    // volume ratio at scoring time
  mkt_cap_b: number    // market cap in 亿 CNY
  stability: number    // consecutive ticks in Top-N
  in_pos: boolean      // currently held
}
