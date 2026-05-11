import { useRef, useEffect } from 'react'
import { TradeInfo } from '../types'

interface Props {
  trades: TradeInfo[]
}

const STATUS_STYLES: Record<string, string> = {
  FILLED: 'text-profit bg-green-950/50',
  PARTIAL: 'text-caution bg-yellow-950/50',
  REJECTED: 'text-loss bg-red-950/50',
}

function fmtTime(ms: number) {
  if (!ms) return '--'
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  const year = d.getFullYear()
  const month = d.getMonth() + 1
  const day = d.getDate()
  const hour = d.getHours()
  const minute = d.getMinutes()
  const second = d.getSeconds()
  return `${year}-${pad(month)}-${pad(day)} ${pad(hour)}:${pad(minute)}:${pad(second)}`
}

export default function TradeStream({ trades }: Props) {
  const bottomRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [trades.length])

  return (
    <div className="card h-full flex flex-col">
      <div className="flex items-center justify-between mb-2">
        <h2 className="text-sm font-semibold text-gray-200">订单历史</h2>
        <span className="text-xs text-gray-500">{trades.length} 笔</span>
      </div>

      <div className="flex-1 overflow-y-auto space-y-0.5 pr-0.5">
        {trades.length === 0 ? (
          <div className="h-full flex items-center justify-center text-sm text-gray-500">
            暂无成交记录
          </div>
        ) : (
          trades.map((t) => {
            const isBuy = t.side === 'BUY'
            const statusPrefix = t.status === 'FILLED' ? '✓' : t.status === 'PARTIAL' ? '~' : '✗'
            const slippageSign = t.slippage_pct >= 0 ? '+' : ''

            return (
              <div
                key={t.order_id}
                className="flex items-center gap-2 py-1.5 px-1.5 rounded hover:bg-surface-hover border border-transparent hover:border-surface-border transition-all text-xs"
              >
                <span
                  className={`px-1.5 py-0.5 rounded font-medium ${
                    isBuy ? 'text-profit bg-green-950/50' : 'text-loss bg-red-950/50'
                  }`}
                >
                  {t.side}
                </span>
                <span className="font-medium text-gray-200">{t.symbol}</span>
                <span className="text-gray-400 tabular-nums">{t.fill_price.toFixed(4)}</span>
                <span
                  className={`tabular-nums ${
                    t.slippage_pct >= 0 ? 'text-profit' : 'text-loss'
                  }`}
                >
                  {slippageSign}
                  {t.slippage_pct.toFixed(2)}%
                </span>
                <span className="text-gray-500 tabular-nums">
                  {t.filled_qty}/{t.qty}
                </span>
                <span
                  className={`px-1.5 py-0.5 rounded font-medium ${
                    STATUS_STYLES[t.status] ?? 'text-gray-400 bg-gray-950/50'
                  }`}
                >
                  {statusPrefix} {t.status}
                </span>
                <span className="text-gray-500 tabular-nums">{t.latency_ms}ms</span>
                <span className="text-gray-600 ml-auto shrink-0 tabular-nums whitespace-nowrap">
                  {fmtTime(t.timestamp)}
                </span>
              </div>
            )
          })
        )}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}
