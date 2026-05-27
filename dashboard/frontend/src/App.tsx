import { useState, useEffect, useRef, useCallback } from 'react'
import { Snapshot, CommandAction } from './types'
import AccountBar from './components/AccountBar'
import EquityChart from './components/EquityChart'
import PositionsTable from './components/PositionsTable'
import PositionHistory from './components/PositionHistory'
import TradeStream from './components/TradeStream'
import SafetyRiskPanel from './components/SafetyRiskPanel'
import MarketPanel from './components/MarketPanel'
import ExecQuality from './components/ExecQuality'
import AlertCenter from './components/AlertCenter'
import ControlPanel from './components/ControlPanel'
import CandidatePool from './components/CandidatePool'
import { Wifi, WifiOff, RefreshCw } from 'lucide-react'

const WS_URL = `ws://${window.location.host}/ws`
const RECONNECT_DELAY = 2000

export default function App() {
  const [snap, setSnap] = useState<Snapshot | null>(null)
  const [connected, setConnected] = useState(false)
  const [lastUpdateMs, setLastUpdateMs] = useState(0)
  const wsRef = useRef<WebSocket | null>(null)
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>()

  const connect = useCallback(() => {
    const ws = new WebSocket(WS_URL)
    wsRef.current = ws

    ws.onopen = () => {
      setConnected(true)
      clearTimeout(reconnectTimer.current)
    }

    ws.onmessage = (e) => {
      try {
        const data: Snapshot = JSON.parse(e.data)
        setSnap(data)
        setLastUpdateMs(Date.now())
      } catch {}
    }

    ws.onclose = () => {
      setConnected(false)
      wsRef.current = null
      reconnectTimer.current = setTimeout(connect, RECONNECT_DELAY)
    }

    ws.onerror = () => {
      ws.close()
    }
  }, [])

  useEffect(() => {
    connect()
    return () => {
      clearTimeout(reconnectTimer.current)
      wsRef.current?.close()
    }
  }, [connect])

  const sendCommand = useCallback((action: CommandAction) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify({ type: 'command', action }))
    }
  }, [])

  const lag = snap ? Math.round((Date.now() - lastUpdateMs) / 1000) : null

  return (
    <div className="min-h-screen bg-surface text-gray-200 flex flex-col gap-2 p-2">

      {/* ── Row 1: Header ──────────────────────────────────────────────── */}
      <header className="flex items-center gap-3 px-1">
        <div className="flex items-center gap-2">
          <span className="text-accent font-semibold text-base tracking-tight">
            ⬡ ASTOCK COCKPIT
          </span>
          <span className="text-gray-600 text-xs">A股量化交易控制面板</span>
        </div>
        <div className="flex-1" />
        <div className={`flex items-center gap-1.5 text-xs ${connected ? 'text-profit' : 'text-loss'}`}>
          {connected ? <Wifi size={13} /> : <WifiOff size={13} />}
          <span>{connected ? 'LIVE' : '重连中…'}</span>
          {lag !== null && lag < 10 && (
            <span className="text-gray-500 ml-1">+{lag}s</span>
          )}
        </div>
        {snap && (
          <span className="text-gray-600 text-xs tabular-nums">
            Tick #{snap.account.tick_count}
          </span>
        )}
      </header>

      {/* ── Row 2: Account Bar ─────────────────────────────────────────── */}
      <AccountBar account={snap?.account ?? null} />

      {/* ── Row 3: Equity Chart + Positions ───────────────────────────── */}
      <div className="flex gap-2" style={{ minHeight: 240 }}>
        <div className="flex-[3] min-w-0">
          <EquityChart equity={snap?.equity ?? []} />
        </div>
        <div className="flex-[2] min-w-0">
          <PositionsTable positions={snap?.positions ?? []} />
        </div>
      </div>

      {/* ── Row 4: Today's Candidate Pool (Top-20) ────────────────────── */}
      <CandidatePool candidates={snap?.candidates ?? []} />

      {/* ── Row 5: Safety/Risk + Market/Strategy + Control ───────────── */}
      <div className="flex gap-2" style={{ minHeight: 200 }}>
        <div className="flex-[2] min-w-0">
          <SafetyRiskPanel safety={snap?.safety ?? null} risk={snap?.risk ?? null} />
        </div>
        <div className="flex-[2] min-w-0">
          <MarketPanel market={snap?.market ?? null} strategies={snap?.strategies ?? []} />
        </div>
        <div className="flex-[2] min-w-0">
          <ControlPanel safety={snap?.safety ?? null} onCommand={sendCommand} />
        </div>
      </div>

      {/* ── Row 6: Position History + Order History ────────────────────── */}
      <div className="flex gap-2" style={{ minHeight: 220 }}>
        <div className="flex-1 min-w-0">
          <PositionHistory trades={snap?.position_history ?? []} />
        </div>
        <div className="flex-1 min-w-0">
          <TradeStream trades={snap?.trades ?? []} />
        </div>
      </div>

      {/* ── Row 7: Exec Quality + Alert Center ────────────────────────── */}
      <div className="flex gap-2">
        <div className="flex-1 min-w-0">
          <ExecQuality exec={snap?.execution ?? null} />
        </div>
        <div className="flex-[2] min-w-0">
          <AlertCenter alerts={snap?.alerts ?? []} />
        </div>
      </div>

      {/* ── Empty state overlay ────────────────────────────────────────── */}
      {!snap && (
        <div className="absolute inset-0 flex items-center justify-center pointer-events-none">
          <div className="flex flex-col items-center gap-3 text-gray-600">
            <RefreshCw size={28} className="animate-spin" />
            <span className="text-sm">等待数据… 请先启动 Paper Trading</span>
            <code className="text-xs bg-surface-card px-3 py-1 rounded border border-surface-border">
              bash scripts/start.sh
            </code>
          </div>
        </div>
      )}
    </div>
  )
}
