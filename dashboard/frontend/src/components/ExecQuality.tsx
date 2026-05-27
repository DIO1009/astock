import { ExecInfo } from '../types'
import { BarChart2 } from 'lucide-react'

interface Props { exec: ExecInfo | null }

function StatRow({ label, value, warn }: { label: string; value: string; warn?: boolean }) {
  return (
    <div className="flex justify-between items-center py-1 border-b border-surface-border/40">
      <span className="text-gray-500 text-xs">{label}</span>
      <span className={`tabular-nums text-xs font-medium ${warn ? 'text-caution' : 'text-gray-300'}`}>{value}</span>
    </div>
  )
}

export default function ExecQuality({ exec }: Props) {
  const e = exec

  return (
    <div className="card">
      <div className="flex items-center gap-1.5 mb-2">
        <BarChart2 size={12} className="text-accent" />
        <span className="card-title mb-0">执行质量</span>
        {e && <span className="ml-auto text-gray-600 text-xs">{e.total_orders} 笔</span>}
      </div>

      {!e || e.total_orders === 0 ? (
        <div className="text-gray-600 text-xs py-2">暂无数据</div>
      ) : (
        <div className="grid grid-cols-2 gap-x-6">
          <div>
            <StatRow label="成交率" value={`${e.fill_rate.toFixed(1)}%`} warn={e.fill_rate < 80} />
            <StatRow label="拒单率" value={`${e.rejection_rate.toFixed(1)}%`} warn={e.rejection_rate > 15} />
            <StatRow label="均滑点" value={`${e.avg_slippage_pct.toFixed(3)}%`} warn={e.avg_slippage_pct > 0.5} />
          </div>
          <div>
            <StatRow label="P50滑点" value={`${e.p50_slippage_pct.toFixed(3)}%`} />
            <StatRow label="P90滑点" value={`${e.p90_slippage_pct.toFixed(3)}%`} warn={e.p90_slippage_pct > 1.0} />
            <StatRow label="均延迟" value={`${e.avg_latency_ms.toFixed(0)}ms`} warn={e.avg_latency_ms > 300} />
          </div>
          <div className="col-span-2">
            <StatRow label="P90延迟" value={`${e.p90_latency_ms.toFixed(0)}ms`} warn={e.p90_latency_ms > 450} />
          </div>
        </div>
      )}
    </div>
  )
}
