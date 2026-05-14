import { SafetyInfo, RiskInfo } from '../types'
import { Shield, Flame, Snowflake, AlertTriangle } from 'lucide-react'

interface Props {
  safety: SafetyInfo | null
  risk: RiskInfo | null
}

const TIER_COLORS: Record<string, string> = {
  NORMAL:  'text-profit',
  CAUTION: 'text-caution',
  REDUCED: 'text-caution',
  DEFENSE: 'text-defense',
  FROZEN:  'text-loss',
}

function StreakBar({ streak, halfAt, freezeAt }: { streak: number; halfAt: number; freezeAt: number }) {
  const safeHalfAt = halfAt > 0 ? halfAt : 10
  const safeFreezeAt = freezeAt > 0 ? freezeAt : 15
  const pct = Math.min(streak / safeFreezeAt, 1)
  const color = streak >= safeFreezeAt ? 'bg-loss' : streak >= safeHalfAt ? 'bg-defense' : streak >= 3 ? 'bg-caution' : 'bg-profit'
  return (
    <div className="space-y-0.5">
      <div className="flex justify-between text-xs">
        <span className="text-gray-500">连续亏损</span>
        <span className={`font-bold tabular-nums ${streak >= safeHalfAt ? 'text-loss' : 'text-gray-300'}`}>{streak} 笔</span>
      </div>
      <div className="w-full h-1.5 bg-surface rounded-full overflow-hidden">
        <div className={`h-full ${color} transition-all duration-500`} style={{ width: `${pct * 100}%` }} />
      </div>
      <div className="flex justify-between text-gray-600 text-xs">
        <span>0</span>
        <span className="text-caution">{safeHalfAt}→半仓</span>
        <span className="text-loss">{safeFreezeAt}→冻结</span>
      </div>
    </div>
  )
}

export default function SafetyRiskPanel({ safety, risk }: Props) {
  const s = safety
  const r = risk

  return (
    <div className="card h-full flex flex-col gap-3">
      {/* ── Safety Guard ─────────────────────── */}
      <div>
        <div className="flex items-center gap-1.5 mb-2">
          <Shield size={12} className="text-accent" />
          <span className="card-title mb-0">安全控制</span>
        </div>

        {s ? (
          <div className="space-y-2">
            <StreakBar streak={s.streak} halfAt={s.streak_half_position_at} freezeAt={s.streak_freeze_at} />

            {/* Scale + Freeze */}
            <div className="flex gap-2 text-xs">
              <div className="flex-1 bg-surface rounded p-2 border border-surface-border">
                <div className="text-gray-500 mb-0.5">仓位倍数</div>
                <div className={`font-bold text-base tabular-nums ${s.streak_scale < 1 ? 'text-loss' : 'text-profit'}`}>
                  {s.streak_scale.toFixed(1)}×
                </div>
              </div>
              <div className="flex-1 bg-surface rounded p-2 border border-surface-border">
                <div className="text-gray-500 mb-0.5">开仓状态</div>
                {s.allow_open ? (
                  <div className="font-bold text-profit text-xs">✓ 允许</div>
                ) : s.freeze_left > 0 ? (
                  <div className="font-bold text-loss text-xs flex items-center gap-1">
                    <Snowflake size={10} />冻结 {s.freeze_left}T
                  </div>
                ) : s.manual_stop_open ? (
                  <div className="font-bold text-caution text-xs">⚠ 人工禁止</div>
                ) : s.trading_stopped ? (
                  <div className="font-bold text-loss text-xs">✗ 异常暂停</div>
                ) : (
                  <div className="font-bold text-loss text-xs">✗ 禁止</div>
                )}
              </div>
              <div className="flex-1 bg-surface rounded p-2 border border-surface-border">
                <div className="text-gray-500 mb-0.5">异常执行</div>
                <div className={`font-bold text-base tabular-nums ${s.abnormal_count >= 3 ? 'text-loss' : s.abnormal_count > 0 ? 'text-caution' : 'text-profit'}`}>
                  {s.abnormal_count}
                </div>
              </div>
            </div>
          </div>
        ) : (
          <div className="text-gray-600 text-xs">未连接</div>
        )}
      </div>

      {/* ── Portfolio Risk Engine ─────────────── */}
      <div className="border-t border-surface-border pt-2">
        <div className="flex items-center gap-1.5 mb-2">
          <Flame size={12} className="text-defense" />
          <span className="card-title mb-0">风险引擎</span>
        </div>

        {r ? (
          <div className="space-y-1.5 text-xs">
            <div className="flex justify-between">
              <span className="text-gray-500">当前分档</span>
              <span className={`font-bold ${TIER_COLORS[r.tier] ?? 'text-gray-300'}`}>{r.tier}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-500">回撤</span>
              <span className="tabular-nums text-loss">{r.drawdown_pct.toFixed(2)}%</span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-500">权益波动</span>
              <span className={`tabular-nums ${r.vol_pct > 2 ? 'text-caution' : 'text-gray-400'}`}>{r.vol_pct.toFixed(2)}%</span>
            </div>
            <div className="flex justify-between">
              <span className="text-gray-500">有效仓位上限</span>
              <span className={`tabular-nums font-semibold ${r.effective_pct < 0.5 ? 'text-loss' : r.effective_pct < 0.7 ? 'text-caution' : 'text-profit'}`}>
                {(r.effective_pct * 100).toFixed(0)}%
              </span>
            </div>
            {r.is_frozen && (
              <div className="flex items-center gap-1 text-loss bg-red-950/30 rounded px-2 py-1">
                <AlertTriangle size={10} />
                <span className="font-semibold">Kill Switch – 剩余 {r.freeze_left} tick</span>
              </div>
            )}
          </div>
        ) : (
          <div className="text-gray-600 text-xs">未连接</div>
        )}
      </div>
    </div>
  )
}
