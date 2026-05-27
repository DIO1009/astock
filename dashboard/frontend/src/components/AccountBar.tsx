import { AccountInfo } from '../types'

const RISK_COLORS: Record<string, string> = {
  NORMAL:    'bg-green-900/40 text-profit border-profit/30',
  CAUTION:   'bg-yellow-900/40 text-caution border-yellow-600/30',
  DEFENSE:   'bg-orange-900/40 text-defense border-orange-600/30',
  EMERGENCY: 'bg-red-900/40 text-loss border-red-600/30',
}

function pct(v: number, digits = 2) {
  const color = v >= 0 ? 'text-profit' : 'text-loss'
  const sign = v >= 0 ? '+' : ''
  return <span className={color}>{sign}{v.toFixed(digits)}%</span>
}

function cny(v: number) {
  return `¥${v.toLocaleString('zh-CN', { minimumFractionDigits: 0, maximumFractionDigits: 0 })}`
}

interface Props { account: AccountInfo | null }

export default function AccountBar({ account }: Props) {
  if (!account) {
    return (
      <div className="card grid grid-cols-7 gap-4 animate-pulse">
        {Array.from({ length: 7 }).map((_, i) => (
          <div key={i} className="h-10 bg-surface-hover rounded" />
        ))}
      </div>
    )
  }

  const riskClass = RISK_COLORS[account.risk_level] ?? RISK_COLORS.NORMAL

  return (
    <div className="card flex items-center gap-6 overflow-x-auto">
      {/* Risk badge */}
      <div className={`badge border px-3 py-1.5 text-sm font-bold shrink-0 ${riskClass}`}>
        {account.risk_level}
      </div>

      <Stat label="总资产" value={<span className="text-accent">{cny(account.total_equity)}</span>} />
      <Stat label="今日收益" value={pct(account.today_return_pct)} />
      <Stat label="累计收益" value={pct(account.total_return_pct)} />
      <Stat label="当前回撤" value={<span className={account.current_drawdown_pct > 5 ? 'text-loss' : 'text-caution'}>{account.current_drawdown_pct.toFixed(2)}%</span>} />
      <Stat label="最大回撤" value={<span className="text-loss">{account.max_drawdown_pct.toFixed(2)}%</span>} />
      <Stat label="仓位比例" value={<PositionBar pct={account.position_pct} />} />

      <div className="border-l border-surface-border pl-4 ml-auto shrink-0 flex gap-5">
        <Stat label="胜率" value={<span className="text-accent">{account.win_rate.toFixed(1)}%</span>} />
        <Stat label="盈亏比" value={<span>{account.profit_factor >= 999 ? '∞' : account.profit_factor.toFixed(2)}</span>} />
        <Stat label="总交易" value={<span className="text-gray-300">{account.trade_count}</span>} />
        <Stat label="现金" value={<span className="text-gray-300">{cny(account.cash)}</span>} />
      </div>
    </div>
  )
}

function Stat({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5 shrink-0">
      <span className="stat-label">{label}</span>
      <span className="stat-value text-lg leading-tight">{value}</span>
    </div>
  )
}

function PositionBar({ pct }: { pct: number }) {
  const clamped = Math.min(100, Math.max(0, pct))
  const color = clamped > 80 ? 'bg-defense' : clamped > 50 ? 'bg-caution' : 'bg-profit'
  return (
    <div className="flex items-center gap-2">
      <div className="w-20 h-2 bg-surface rounded-full overflow-hidden">
        <div className={`h-full ${color} transition-all duration-500`} style={{ width: `${clamped}%` }} />
      </div>
      <span className="text-gray-300 text-sm tabular-nums">{pct.toFixed(1)}%</span>
    </div>
  )
}
