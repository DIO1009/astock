import { PositionInfo } from '../types'
import { ShieldAlert } from 'lucide-react'

interface Props { positions: PositionInfo[] }

function fmt(v: number, digits = 4) {
  return v.toFixed(digits)
}

function fmtMoney(v: number) {
  const sign = v > 0 ? '+' : ''
  return `${sign}${v.toLocaleString('zh-CN', { maximumFractionDigits: 0 })}`
}

function pnlClass(v: number) {
  if (v > 0) return 'text-profit'
  if (v < 0) return 'text-loss'
  return 'text-gray-400'
}

export default function PositionsTable({ positions }: Props) {
  return (
    <div className="card h-full flex flex-col">
      <div className="flex items-center justify-between mb-2">
        <span className="card-title mb-0">当前持仓</span>
        <span className="text-xs text-gray-500">{positions.length} 只</span>
      </div>

      {positions.length === 0 ? (
        <div className="flex-1 flex items-center justify-center text-gray-600 text-sm">
          空仓
        </div>
      ) : (
        <div className="flex-1 overflow-auto">
          <table className="w-full text-xs">
            <thead>
              <tr className="text-gray-500 border-b border-surface-border">
                <th className="text-left pb-1.5 pr-2">代码</th>
                <th className="text-right pb-1.5 pr-2">数量</th>
                <th className="text-right pb-1.5 pr-2">成本</th>
                <th className="text-right pb-1.5 pr-2">现价</th>
                <th className="text-right pb-1.5 pr-2">浮盈%</th>
                <th className="text-right pb-1.5 pr-2">浮盈额</th>
                <th className="text-right pb-1.5">市值 ¥</th>
              </tr>
            </thead>
            <tbody>
              {positions.map(p => (
                <tr
                  key={p.symbol}
                  className={`border-b border-surface-border/50 hover:bg-surface-hover transition-colors
                    ${p.defense_flag ? 'bg-red-950/20' : ''}`}
                >
                  <td className="py-2 pr-2">
                    <div className="flex items-center gap-1">
                      {p.defense_flag && (
                        <ShieldAlert size={11} className="text-defense shrink-0" />
                      )}
                      <span className="font-semibold text-gray-200">{p.symbol}</span>
                    </div>
                  </td>
                  <td className="text-right pr-2 tabular-nums text-gray-300">{p.quantity}</td>
                  <td className="text-right pr-2 tabular-nums text-gray-400">{fmt(p.avg_price)}</td>
                  <td className="text-right pr-2 tabular-nums text-gray-200">{fmt(p.current_price)}</td>
                  <td className={`text-right pr-2 tabular-nums font-semibold ${pnlClass(p.pnl_pct)}`}>
                    {p.pnl_pct >= 0 ? '+' : ''}{p.pnl_pct.toFixed(2)}%
                  </td>
                  <td className={`text-right pr-2 tabular-nums font-semibold ${pnlClass(p.pnl_abs)}`}>
                    {fmtMoney(p.pnl_abs)}
                  </td>
                  <td className="text-right tabular-nums text-gray-300">
                    {p.market_value.toLocaleString('zh-CN', { maximumFractionDigits: 0 })}
                  </td>
                </tr>
              ))}
            </tbody>
            {positions.length > 0 && (
              <tfoot>
                <tr className="border-t border-surface-border">
                  <td colSpan={6} className="pt-1.5 text-gray-500">总持仓市值</td>
                  <td className="text-right pt-1.5 tabular-nums text-accent font-semibold">
                    {positions.reduce((s, p) => s + p.market_value, 0)
                      .toLocaleString('zh-CN', { maximumFractionDigits: 0 })}
                  </td>
                </tr>
              </tfoot>
            )}
          </table>
        </div>
      )}
    </div>
  )
}
