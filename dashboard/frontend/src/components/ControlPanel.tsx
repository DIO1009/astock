import { useState } from 'react'
import { SafetyInfo, CommandAction } from '../types'
import { StopCircle, PlayCircle, AlertOctagon, CheckCircle2 } from 'lucide-react'

interface Props {
  safety: SafetyInfo | null
  onCommand: (action: CommandAction) => void
}

type ConfirmState = CommandAction | null

export default function ControlPanel({ safety, onCommand }: Props) {
  const [confirm, setConfirm] = useState<ConfirmState>(null)

  function handleClick(action: CommandAction) {
    // force_liquidate requires double-confirm; others single
    if (action === 'force_liquidate' && confirm !== action) {
      setConfirm(action)
      setTimeout(() => setConfirm(null), 5000)
      return
    }
    onCommand(action)
    setConfirm(null)
  }

  const openingBlocked = safety ? (
    !safety.allow_open ||
    safety.manual_stop_open ||
    safety.freeze_left > 0 ||
    safety.trading_stopped
  ) : false
  const halfAt = safety && safety.streak_half_position_at > 0 ? safety.streak_half_position_at : 10

  return (
    <div className="card h-full flex flex-col gap-3">
      <span className="card-title">控制面板</span>

      {/* ── Buttons ────────────────────────────── */}
      <div className="flex flex-col gap-2">

        {/* Stop Opening */}
        <button
          onClick={() => handleClick('stop_opening')}
          disabled={openingBlocked}
          className="btn flex items-center gap-2 bg-yellow-900/30 text-caution
                     border border-yellow-700/40 hover:bg-yellow-900/60 hover:border-yellow-600
                     disabled:opacity-40"
        >
          <StopCircle size={14} />
          停止开仓
          {safety?.manual_stop_open && (
            <span className="ml-auto text-xs bg-caution/20 px-1.5 rounded">已激活</span>
          )}
        </button>

        {/* Resume Opening */}
        <button
          onClick={() => handleClick('resume_opening')}
          disabled={!openingBlocked || (safety?.freeze_left ?? 0) > 0}
          className="btn flex items-center gap-2 bg-green-900/20 text-profit
                     border border-green-700/30 hover:bg-green-900/40 hover:border-green-600
                     disabled:opacity-40"
        >
          <PlayCircle size={14} />
          恢复开仓
        </button>

        {/* Force Liquidate */}
        <button
          onClick={() => handleClick('force_liquidate')}
          className={`btn flex items-center gap-2 border
            ${confirm === 'force_liquidate'
              ? 'bg-red-800/60 border-red-500 text-white animate-pulse'
              : 'bg-red-950/30 text-loss border-red-800/40 hover:bg-red-950/60 hover:border-red-700'
            }`}
        >
          <AlertOctagon size={14} />
          {confirm === 'force_liquidate' ? '⚠ 再次点击确认清仓！' : '全部清仓'}
        </button>
      </div>

      {/* ── Safety quick status ─────────────────── */}
      {safety && (
        <div className="border-t border-surface-border pt-2 space-y-1 text-xs">
          <StatusRow
            label="开仓许可"
            ok={safety.allow_open}
            yes="✓ 允许"
            no={
              safety.freeze_left > 0 ? `冻结 ${safety.freeze_left}T` :
              safety.manual_stop_open ? '人工禁止' :
              safety.trading_stopped ? '异常暂停' : '禁止'
            }
          />
          <StatusRow
            label="强平待执行"
            ok={!safety.force_liq_pending}
            yes="否"
            no="⚠ 待执行"
          />
          <div className="flex justify-between text-gray-500">
            <span>连续亏损笔数</span>
            <span className={`tabular-nums font-semibold ${
              safety.streak >= halfAt ? 'text-loss' :
              safety.streak >= 3 ? 'text-caution' : 'text-gray-400'
            }`}>{safety.streak}</span>
          </div>
        </div>
      )}

      {/* ── Keyboard hint ──────────────────────── */}
      <div className="mt-auto text-gray-600 text-xs border-t border-surface-border pt-2 space-y-0.5">
        <div>kill -USR1 → 停止开仓</div>
        <div>kill -USR2 → 全部清仓</div>
        <div>kill -HUP  → 恢复开仓</div>
      </div>
    </div>
  )
}

function StatusRow({
  label, ok, yes, no
}: { label: string; ok: boolean; yes: string; no: string }) {
  return (
    <div className="flex justify-between items-center">
      <span className="text-gray-500">{label}</span>
      <div className={`flex items-center gap-1 ${ok ? 'text-profit' : 'text-loss'}`}>
        {ok ? <CheckCircle2 size={10} /> : <AlertOctagon size={10} />}
        <span className="font-medium">{ok ? yes : no}</span>
      </div>
    </div>
  )
}
