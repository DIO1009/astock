import { MarketInfo, StrategyInfo } from '../types'
import { RadarChart, PolarGrid, PolarAngleAxis, Radar, ResponsiveContainer, Tooltip } from 'recharts'
import { TrendingUp, TrendingDown, Minus } from 'lucide-react'

interface Props {
  market: MarketInfo | null
  strategies: StrategyInfo[]
}

const STATE_CONFIG: Record<string, { icon: React.ReactNode; color: string; bg: string }> = {
  UPTREND:   { icon: <TrendingUp size={14} />,  color: 'text-profit',  bg: 'bg-green-950/40 border-profit/30' },
  OSCILLATE: { icon: <Minus size={14} />,        color: 'text-caution', bg: 'bg-yellow-950/40 border-yellow-600/30' },
  DOWNTREND: { icon: <TrendingDown size={14} />, color: 'text-loss',    bg: 'bg-red-950/40 border-red-600/30' },
}

export default function MarketPanel({ market, strategies }: Props) {
  const cfg = STATE_CONFIG[market?.state ?? ''] ?? STATE_CONFIG.OSCILLATE

  // Radar data: normalize weights to [0,1] for display
  const maxW = Math.max(...strategies.map(s => s.weight), 0.001)
  const radarData = strategies.map(s => ({
    name: s.name.replace('strategy', ''),
    value: s.weight / maxW,
  }))

  return (
    <div className="card h-full flex flex-col gap-3">
      {/* ── Market state ─────────────────────── */}
      <div>
        <span className="card-title">市场状态</span>
        <div className={`flex items-center gap-2 px-3 py-2 rounded border ${cfg.bg} ${cfg.color}`}>
          {cfg.icon}
          <span className="font-bold">{market?.state ?? '---'}</span>
          {market?.index_price ? (
            <span className="ml-auto text-gray-400 text-xs tabular-nums">
              沪深300 {market.index_price.toFixed(2)}
            </span>
          ) : null}
        </div>
      </div>

      {/* ── Strategy weights ─────────────────── */}
      <div className="flex-1">
        <span className="card-title">策略权重</span>

        {strategies.length === 0 ? (
          <div className="text-gray-600 text-xs">暂无数据</div>
        ) : (
          <div className="space-y-1.5">
            {strategies.map(s => (
              <div key={s.name} className="text-xs">
                <div className="flex justify-between mb-0.5">
                  <span className="text-gray-400 capitalize">{s.name}</span>
                  <div className="flex gap-2 text-gray-500">
                    <span className={s.avg_pnl >= 0 ? 'text-profit' : 'text-loss'}>
                      {s.avg_pnl >= 0 ? '+' : ''}{s.avg_pnl.toFixed(2)}%
                    </span>
                    <span>{s.trades}笔</span>
                  </div>
                </div>
                <div className="flex items-center gap-1.5">
                  <div className="flex-1 h-1 bg-surface rounded-full overflow-hidden">
                    <div
                      className="h-full bg-accent transition-all duration-700"
                      style={{ width: `${(s.weight / maxW) * 100}%` }}
                    />
                  </div>
                  <span className="tabular-nums text-accent w-10 text-right">
                    {(s.weight * 100).toFixed(0)}%
                  </span>
                  {s.weight !== s.base_weight && (
                    <span className={`text-xs ${s.weight > s.base_weight ? 'text-profit' : 'text-loss'}`}>
                      {s.weight > s.base_weight ? '▲' : '▼'}
                    </span>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
