import { ClosedTradeInfo } from '../types'

interface Props { trades: ClosedTradeInfo[] }

const REASON_STYLES: Record<string, string> = {
  止损: 'text-loss bg-red-950/50',
  止盈: 'text-profit bg-green-950/50',
  追踪止盈: 'text-caution bg-yellow-950/50',
  轮动调仓: 'text-gray-300 bg-slate-800/60',
  卖出: 'text-gray-300 bg-slate-800/60',
}

function fmtTime(ms: number) {
  if (!ms) return '--'
  return new Date(ms).toLocaleString('zh-CN', {
    hour12: false, month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit',
  })
}

function pnlClass(v: number) {
  if (v > 0) return 'text-profit'
  if (v < 0) return 'text-loss'
  return 'text-gray-400'
}

function fmtMoney(v: number) {
  const sign = v > 0 ? '+' : ''
  return `${sign}${v.toLocaleString('zh-CN', { maximumFractionDigits: 0 })}`
}

export default function PositionHistory({ trades }: Props) {
  return (
    <div className="card h-full flex flex-col">
      <div className="flex items-center justify-between mb-2">
        <span className="card-title mb-0">持仓历史</span>
        <span className="text-xs text-gray-500">{trades.length} 笔</span>
      </div>

      <div className="flex-1 overflow-y-auto">
        {trades.length === 0 ? (
          <div className="flex items-center justify-center h-16 text-gray-600 text-xs">
            暂无已平仓记录
          </div>
        ) : (
          <table className="w-full text-xs">
            <thead>
              <tr className="text-gray-500 border-b border-surface-border sticky top-0 bg-surface-card">
                <th className="text-left pb-1.5 pr-2">代码</th>
                <th className="text-right pb-1.5 pr-2">成本</th>
                <th className="text-right pb-1.5 pr-2">出场</th>
                <th className="text-right pb-1.5 pr-2">盈亏%</th>
                <th className="text-right pb-1.5 pr-2">盈亏额</th>
                <th className="text-right pb-1.5 pr-2">持仓</th>
                <th className="text-center pb-1.5 pr-2">原因</th>
                <th className="text-right pb-1.5">时间</th>
              </tr>
            </thead>
            <tbody>
              {trades.map((t, idx) => (
                <tr
                  key={`${t.symbol}-${t.timestamp}-${idx}`}
                  className="border-b border-surface-border/40 hover:bg-surface-hover transition-colors"
                >
                  <td className="py-1.5 pr-2 font-semibold text-gray-200">{t.symbol}</td>
                  <td className="py-1.5 pr-2 text-right tabular-nums text-gray-400">
                    {t.entry_price.toFixed(4)}
                  </td>
                  <td className="py-1.5 pr-2 text-right tabular-nums text-gray-300">
                    {t.exit_price.toFixed(4)}
                  </td>
                  <td className={`py-1.5 pr-2 text-right tabular-nums font-semibold ${pnlClass(t.pnl_pct)}`}>
                    {t.pnl_pct >= 0 ? '+' : ''}{t.pnl_pct.toFixed(2)}%
                  </td>
                  <td className={`py-1.5 pr-2 text-right tabular-nums font-semibold ${pnlClass(t.pnl_abs)}`}>
                    {fmtMoney(t.pnl_abs)}
                  </td>
                  <td className="py-1.5 pr-2 text-right tabular-nums text-gray-500">
                    {t.hold_ticks}T
                  </td>
                  <td className="py-1.5 pr-2 text-center">
                    <span className={`badge ${REASON_STYLES[t.exit_reason] ?? 'text-gray-300 bg-slate-800/60'}`}>{t.exit_reason}</span>
                  </td>
                  <td className="py-1.5 text-right tabular-nums text-gray-600 whitespace-nowrap">
                    {fmtTime(t.timestamp)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
