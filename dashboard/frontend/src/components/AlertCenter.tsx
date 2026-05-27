import { AlertInfo } from '../types'
import { Bell, AlertTriangle, ShieldAlert, Zap } from 'lucide-react'

interface Props { alerts: AlertInfo[] }

const LEVEL_CONFIG: Record<string, { icon: React.ReactNode; cls: string }> = {
  CAUTION:   { icon: <Zap size={11} />,           cls: 'text-caution bg-yellow-950/30 border-yellow-800/40' },
  DEFENSE:   { icon: <ShieldAlert size={11} />,   cls: 'text-defense bg-orange-950/30 border-orange-800/40' },
  EMERGENCY: { icon: <AlertTriangle size={11} />, cls: 'text-loss bg-red-950/40 border-red-800/40' },
  ANOMALY:   { icon: <Zap size={11} />,           cls: 'text-caution bg-yellow-950/20 border-yellow-900/30' },
}

function fmtTime(ms: number) {
  return new Date(ms).toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

export default function AlertCenter({ alerts }: Props) {
  const sorted = [...alerts].reverse() // newest first

  return (
    <div className="card flex flex-col" style={{ maxHeight: 180 }}>
      <div className="flex items-center gap-1.5 mb-2 shrink-0">
        <Bell size={12} className="text-caution" />
        <span className="card-title mb-0">告警中心</span>
        {alerts.length > 0 && (
          <span className="badge bg-caution/20 text-caution border border-caution/30 ml-1">
            {alerts.length}
          </span>
        )}
      </div>

      {sorted.length === 0 ? (
        <div className="text-gray-600 text-xs py-2 flex items-center gap-2">
          <span className="text-profit">✓</span> 暂无告警
        </div>
      ) : (
        <div className="overflow-y-auto space-y-1">
          {sorted.map((a, i) => {
            const cfg = LEVEL_CONFIG[a.level] ?? LEVEL_CONFIG.CAUTION
            return (
              <div
                key={i}
                className={`flex items-start gap-2 px-2 py-1.5 rounded border text-xs ${cfg.cls}`}
              >
                <span className="shrink-0 mt-0.5">{cfg.icon}</span>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-semibold">{a.level}</span>
                    {a.drawdown > 0 && (
                      <span className="text-loss">回撤 {a.drawdown.toFixed(2)}%</span>
                    )}
                    <span className="ml-auto text-gray-500 tabular-nums shrink-0">{fmtTime(a.timestamp)}</span>
                  </div>
                  <div className="text-gray-400 truncate mt-0.5">{a.message}</div>
                </div>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
