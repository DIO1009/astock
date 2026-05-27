import { CandidateInfo } from '../types'
import { Briefcase, Signal } from 'lucide-react'

interface Props {
  candidates: CandidateInfo[]
}

function Pct({ v, digits = 2 }: { v: number; digits?: number }) {
  const sign = v > 0 ? '+' : ''
  return (
    <span className={v > 0 ? 'text-profit' : v < 0 ? 'text-loss' : 'text-gray-500'}>
      {sign}{v.toFixed(digits)}%
    </span>
  )
}

function ScoreBar({ score, range = 1 }: { score: number; range?: number }) {
  // range=1 for live_score [-1,+1]; range=3 for daily score [-3,+3]
  const pct = Math.max(0, Math.min(100, (score / range + 1) * 50))
  const color = score > 0.05 ? 'bg-profit' : score < -0.05 ? 'bg-loss' : 'bg-caution'
  return (
    <div className="flex items-center gap-1.5 w-full">
      <div className="flex-1 h-1 bg-surface rounded-full overflow-hidden">
        <div className={`h-full ${color} transition-all duration-700`} style={{ width: `${pct}%` }} />
      </div>
      <span className={`tabular-nums text-xs w-14 text-right ${score >= 0 ? 'text-profit' : 'text-loss'}`}>
        {score >= 0 ? '+' : ''}{score.toFixed(4)}
      </span>
    </div>
  )
}

/** Breakdown tooltip: show per-strategy scores on hover */
function BreakdownTooltip({ breakdown }: { breakdown: Record<string, number> | null }) {
  if (!breakdown) return <span className="text-gray-600 text-xs">—</span>

  const values = Object.values(breakdown)
  const avg = values.length > 0 ? values.reduce((s, v) => s + v, 0) / values.length : 0
  const entries = Object.entries(breakdown).sort(([, a], [, b]) => b - a)

  return (
    <div className="group relative inline-block w-full">
      <ScoreBar score={avg} range={1} />
      <div className="hidden group-hover:block absolute z-50 left-0 top-full mt-1 bg-surface-card border border-surface-border rounded shadow-lg p-2 min-w-[140px]">
        <div className="text-xs font-semibold text-gray-400 mb-1">策略分解</div>
        {entries.map(([name, v]) => (
          <div key={name} className="flex justify-between gap-4 text-xs py-0.5">
            <span className="text-gray-400">{name}</span>
            <span className={v >= 0 ? 'text-profit' : 'text-loss'}>{v >= 0 ? '+' : ''}{v.toFixed(4)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

function StabilityDots({ count }: { count: number }) {
  const MAX = 5
  return (
    <div className="flex items-center gap-0.5">
      {Array.from({ length: MAX }).map((_, i) => (
        <div
          key={i}
          className={`w-1.5 h-1.5 rounded-full ${i < count ? 'bg-accent' : 'bg-surface-border'}`}
        />
      ))}
      {count > MAX && <span className="text-accent text-xs ml-0.5">{count}</span>}
    </div>
  )
}

export default function CandidatePool({ candidates }: Props) {
  const today = new Date().toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })

  return (
    <div className="card flex flex-col gap-2">
      {/* ── Header ─────────────────────────────── */}
      <div className="flex items-center gap-2">
        <span className="card-title">今日候选池</span>
        <span className="text-gray-600 text-xs">Top-50 · {today}</span>
        <span className="text-gray-600 text-xs">({candidates.length} 只)</span>
        <div className="flex-1" />
        <div className="flex items-center gap-3 text-xs text-gray-600">
          <span className="flex items-center gap-1"><Briefcase size={11} /> 持仓</span>
          <span className="flex items-center gap-1"><Signal size={11} /> 稳定性</span>
        </div>
      </div>

      {/* ── Empty state ─────────────────────────── */}
      {candidates.length === 0 ? (
        <div className="text-gray-600 text-xs py-4 text-center">
          暂无候选数据 — 等待今日选股完成（每天 09:00 自动运行）
        </div>
      ) : (
        /* Fixed-height scrollable container; thead is sticky so column headers
           remain visible while the body scrolls vertically. */
        <div className="overflow-x-auto overflow-y-auto" style={{ height: '520px' }}>
          <table className="w-full text-xs tabular-nums">
            <thead className="sticky top-0 z-10 bg-surface-card">
              <tr className="text-gray-600 border-b border-surface-border">
                <th className="text-left pb-1.5 pr-2 w-6">#</th>
                <th className="text-left pb-1.5 pr-2 w-16">代码</th>
                <th className="text-left pb-1.5 pr-3 max-w-[90px]">名称</th>
                <th className="text-left pb-1.5 pr-3 min-w-[130px]" title="每日选股算法得分（日频，来自数据库）">
                  选股分
                </th>
                <th className="text-left pb-1.5 pr-4 min-w-[130px]" title="实时 Tick 策略综合得分（悬停查看各策略分解）">
                  实时分 ↗
                </th>
                <th className="text-right pb-1.5 pr-3">最新价</th>
                <th className="text-right pb-1.5 pr-3">今涨跌</th>
                <th className="text-right pb-1.5 pr-3">5日涨</th>
                <th className="text-right pb-1.5 pr-3">量比</th>
                <th className="text-right pb-1.5 pr-3">市值(亿)</th>
                <th className="text-center pb-1.5 pr-2">稳定性</th>
                <th className="text-center pb-1.5">状态</th>
              </tr>
            </thead>
            <tbody>
              {candidates.map(c => (
                <tr
                  key={c.symbol}
                  className={`border-b border-surface-border/40 hover:bg-surface-card/50 transition-colors ${c.in_pos ? 'bg-accent/5' : ''}`}
                >
                  {/* Rank */}
                  <td className="py-1.5 pr-2 text-gray-600">{c.rank}</td>

                  {/* Symbol */}
                  <td className="py-1.5 pr-2 font-mono text-accent font-medium">{c.symbol}</td>

                  {/* Name */}
                  <td className="py-1.5 pr-3 text-gray-300 max-w-[90px] truncate" title={c.name}>
                    {c.name || '—'}
                  </td>

                  {/* Daily alpha score bar (range ≈ -3 .. +3) */}
                  <td className="py-1.5 pr-3">
                    <ScoreBar score={c.score} range={3} />
                  </td>

                  {/* Live tick score bar with breakdown tooltip (range -1 .. +1) */}
                  <td className="py-1.5 pr-4">
                    {c.live_score !== 0 || c.breakdown
                      ? <BreakdownTooltip breakdown={c.breakdown} />
                      : <span className="text-gray-600">—</span>
                    }
                  </td>

                  {/* Live price */}
                  <td className="py-1.5 pr-3 text-right text-gray-200">
                    {c.price > 0 ? c.price.toFixed(2) : '—'}
                  </td>

                  {/* Today % chg */}
                  <td className="py-1.5 pr-3 text-right">
                    {c.pct_chg !== 0 ? <Pct v={c.pct_chg} /> : (
                      <span className="text-gray-600">—</span>
                    )}
                  </td>

                  {/* 5d return */}
                  <td className="py-1.5 pr-3 text-right">
                    {c.ret5d !== 0 ? <Pct v={c.ret5d} /> : (
                      <span className="text-gray-600">—</span>
                    )}
                  </td>

                  {/* Volume ratio */}
                  <td className={`py-1.5 pr-3 text-right ${c.vol_ratio > 1.5 ? 'text-profit' : c.vol_ratio > 0 ? 'text-gray-300' : 'text-gray-600'}`}>
                    {c.vol_ratio > 0 ? c.vol_ratio.toFixed(2) : '—'}
                  </td>

                  {/* Market cap */}
                  <td className="py-1.5 pr-3 text-right text-gray-400">
                    {c.mkt_cap_b > 0 ? c.mkt_cap_b.toFixed(0) : '—'}
                  </td>

                  {/* Stability dots */}
                  <td className="py-1.5 pr-2 text-center">
                    <div className="flex justify-center">
                      <StabilityDots count={c.stability} />
                    </div>
                  </td>

                  {/* Status badge */}
                  <td className="py-1.5 text-center">
                    {c.in_pos ? (
                      <span className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded text-xs bg-accent/20 text-accent border border-accent/30">
                        <Briefcase size={9} />
                        持仓
                      </span>
                    ) : (
                      <span className="text-gray-600">观察</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
