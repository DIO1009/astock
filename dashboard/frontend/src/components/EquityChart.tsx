import { useState } from 'react'
import {
  AreaChart, Area, LineChart, Line, XAxis, YAxis,
  CartesianGrid, Tooltip, Legend, ResponsiveContainer, ReferenceLine
} from 'recharts'
import { EquityPoint } from '../types'

type Range = 'all' | '50' | '20'

interface Props { equity: EquityPoint[] }

export default function EquityChart({ equity }: Props) {
  const [range, setRange] = useState<Range>('all')

  const sliced = (() => {
    if (range === '20') return equity.slice(-20)
    if (range === '50') return equity.slice(-50)
    return equity
  })()

  const maxDD = sliced.length ? Math.max(...sliced.map(p => p.drawdown)) : 0

  return (
    <div className="card h-full flex flex-col">
      <div className="flex items-center justify-between mb-2">
        <span className="card-title mb-0">权益曲线</span>
        <div className="flex gap-1">
          {(['20', '50', 'all'] as Range[]).map(r => (
            <button
              key={r}
              onClick={() => setRange(r)}
              className={`px-2 py-0.5 rounded text-xs font-medium transition-colors
                ${range === r ? 'bg-accent/20 text-accent border border-accent/30' : 'text-gray-500 hover:text-gray-300'}`}
            >
              {r === 'all' ? '全部' : r === '50' ? '50T' : '20T'}
            </button>
          ))}
        </div>
      </div>

      <div className="flex-1 min-h-0">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={sliced} margin={{ top: 4, right: 4, left: -10, bottom: 0 }}>
            <defs>
              <linearGradient id="equityGrad" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#58a6ff" stopOpacity={0.3} />
                <stop offset="95%" stopColor="#58a6ff" stopOpacity={0.02} />
              </linearGradient>
              <linearGradient id="ddGrad" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#f85149" stopOpacity={0.4} />
                <stop offset="95%" stopColor="#f85149" stopOpacity={0.02} />
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="#21262d" vertical={false} />
            <XAxis dataKey="tick" tick={{ fill: '#6e7681', fontSize: 10 }} tickLine={false} axisLine={false} />
            <YAxis
              yAxisId="equity"
              orientation="left"
              tick={{ fill: '#6e7681', fontSize: 10 }}
              tickLine={false}
              axisLine={false}
              tickFormatter={v => `¥${(v / 1000).toFixed(0)}k`}
            />
            <YAxis
              yAxisId="dd"
              orientation="right"
              tick={{ fill: '#f85149', fontSize: 10 }}
              tickLine={false}
              axisLine={false}
              tickFormatter={v => `${v.toFixed(1)}%`}
              domain={[0, Math.max(maxDD * 1.5, 1)]}
            />
            <Tooltip
              contentStyle={{ background: '#161b22', border: '1px solid #21262d', borderRadius: 6, fontSize: 11 }}
              labelStyle={{ color: '#8b949e' }}
              formatter={(value: number, name: string) => {
                if (name === 'equity') return [`¥${value.toLocaleString('zh-CN', { maximumFractionDigits: 0 })}`, '权益']
                return [`${value.toFixed(2)}%`, '回撤']
              }}
            />
            <Legend
              formatter={v => v === 'equity' ? '权益' : '回撤'}
              wrapperStyle={{ fontSize: 11, color: '#8b949e' }}
            />
            <Area yAxisId="equity" type="monotone" dataKey="equity"
              stroke="#58a6ff" strokeWidth={1.5} fill="url(#equityGrad)" dot={false} />
            <Area yAxisId="dd" type="monotone" dataKey="drawdown"
              stroke="#f85149" strokeWidth={1} fill="url(#ddGrad)" dot={false} />
            {sliced.length > 0 && (
              <ReferenceLine yAxisId="equity" y={sliced[0].equity}
                stroke="#30363d" strokeDasharray="4 4" />
            )}
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}
