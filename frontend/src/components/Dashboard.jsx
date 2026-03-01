import React, { useState, useEffect, useCallback, useMemo } from 'react'
import { XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, AreaChart, Area } from 'recharts'
import { useWebSocketEvent } from '../hooks/useWebSocket'
import CustomSelect from './CustomSelect'

const API_BASE = '/api'

const SIGNAL_COLORS = {
  STRONG_BUY: '#52b788',
  BUY: '#95d5b2',
  NEUTRAL: '#888',
  SELL: '#f4978e',
  STRONG_SELL: '#e63946',
}

const PERIOD_OPTIONS = [
  { value: '1h', label: '1h' },
  { value: '4h', label: '4h' },
  { value: '6h', label: '6h' },
  { value: '12h', label: '12h' },
  { value: '24h', label: '24h' },
  { value: '3d', label: '3 days' },
]

const periodToMs = (period) => {
  switch (period) {
    case '1h':
      return 60 * 60 * 1000
    case '4h':
      return 4 * 60 * 60 * 1000
    case '6h':
      return 6 * 60 * 60 * 1000
    case '12h':
      return 12 * 60 * 60 * 1000
    case '24h':
      return 24 * 60 * 60 * 1000
    case '3d':
      return 3 * 24 * 60 * 60 * 1000
    default:
      return 4 * 60 * 60 * 1000
  }
}

const aggregateSnapshots = (data, maxPoints) => {
  if (!Array.isArray(data) || data.length <= maxPoints) {
    return data
  }
  const sorted = [...data].sort((a, b) => new Date(a.timestamp) - new Date(b.timestamp))
  const startTime = new Date(sorted[0].timestamp).getTime()
  const endTime = new Date(sorted[sorted.length - 1].timestamp).getTime()
  const range = Math.max(endTime - startTime, 1)
  const bucketSize = Math.max(Math.ceil(range / maxPoints), 60 * 1000)
  const buckets = new Map()

  for (const snapshot of sorted) {
    const time = new Date(snapshot.timestamp).getTime()
    const bucket = Math.floor((time - startTime) / bucketSize)
    const currentValue = Number(snapshot.total_value)
    const existing = buckets.get(bucket)
    if (!existing || currentValue > existing.total_value) {
      buckets.set(bucket, {
        timestamp: snapshot.timestamp,
        total_value: currentValue,
      })
    }
  }

  return Array.from(buckets.keys())
    .sort((a, b) => a - b)
    .map((key) => buckets.get(key))
}

function Dashboard({ wallet: propWallet, positions: propPositions }) {
  const [orders, setOrders] = useState([])
  const [recentCoins, setRecentCoins] = useState([])
  const [selectedSymbol, setSelectedSymbol] = useState(null)
  const [snapshots, setSnapshots] = useState([])
  const [selectedPeriod, setSelectedPeriod] = useState('4h')
  const [wallet, setWallet] = useState(propWallet || { balance: 0, currency: 'USDT' })
  const [positions, setPositions] = useState(propPositions || [])

  // Sync with props
  useEffect(() => {
    if (propWallet) setWallet(propWallet)
  }, [propWallet])

  useEffect(() => {
    if (propPositions) setPositions(propPositions)
  }, [propPositions])

  // Define fetch functions
  const fetchRecentCoins = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/trending/recent`)
      if (res.ok) {
        const data = await res.json()
        const coins = data.coins || []
        setRecentCoins(coins)
        if (coins.length > 0 && selectedSymbol === null) setSelectedSymbol(coins[0].symbol)
      }
    } catch (err) {}
  }, [selectedSymbol])

  const fetchSnapshots = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/wallet/snapshots?period=${selectedPeriod}`)
      if (res.ok) {
        const data = await res.json()
        setSnapshots(data)
      }
    } catch (err) {}
  }, [selectedPeriod])

  const fetchOrders = useCallback(async () => {
    try {
      const res = await fetch(`${API_BASE}/orders?limit=10`)
      if (res.ok) setOrders(await res.json())
    } catch (err) {}
  }, [])

  useEffect(() => {
    fetchOrders()
    fetchRecentCoins()
  }, [fetchOrders, fetchRecentCoins])

  useEffect(() => {
    fetchSnapshots()
  }, [fetchSnapshots])

  // Listen for WebSocket updates
  useWebSocketEvent('wallet_update', useCallback((data) => {
    setWallet(prev => ({
      ...prev,
      balance: data.balance ?? prev.balance,
      currency: data.currency ?? prev.currency
    }))
    fetchSnapshots()
  }, [fetchSnapshots]))

  useWebSocketEvent('positions_update', useCallback((data) => {
    if (Array.isArray(data)) {
      setPositions(data)
    }
  }, []))

  useWebSocketEvent('position_update', useCallback((data) => {
    setPositions(prev => prev.map(p => 
      p.symbol === data.symbol 
        ? { ...p, ...data }
        : p
    ))
  }, []))

  useWebSocketEvent('snapshot_update', useCallback(() => {
    fetchSnapshots()
  }, [fetchSnapshots]))

  useWebSocketEvent('trending_update', useCallback((data) => {
    if (Array.isArray(data)) {
      setRecentCoins(data)
    } else if (data.coins) {
      setRecentCoins(data.coins)
    }
  }, []))

  useWebSocketEvent('orders_update', useCallback((data) => {
    if (Array.isArray(data)) {
      setOrders(data)
    }
  }, []))

  const openPositions = positions.filter(p => p.status === 'open')
  const totalPositionsValue = openPositions.reduce((sum, p) => sum + (p.current_price || 0) * p.amount, 0)
  const totalPnL = openPositions.reduce((sum, p) => sum + (p.pnl || 0), 0)
  const totalValue = wallet.balance + totalPositionsValue

  const selectedCoin = recentCoins.find(c => c.symbol === selectedSymbol) || null
  const analysis = selectedCoin ? {
    current_price: selectedCoin.price,
    final_signal: selectedCoin.signal,
    rating: selectedCoin.rating,
    timestamp: selectedCoin.timestamp,
    indicators: selectedCoin.indicators || []
  } : null

  // Format data for Recharts
  const periodMs = periodToMs(selectedPeriod)
  const maxChartPoints = periodMs >= 24 * 60 * 60 * 1000 ? 120 : 80
  const aggregatedSnapshots = useMemo(
    () => aggregateSnapshots(snapshots, maxChartPoints),
    [snapshots, maxChartPoints]
  )
  const chartData = aggregatedSnapshots.map(s => ({
    time: new Date(s.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    value: Number(s.total_value),
    timestamp: s.timestamp,
  }))

  const CustomTooltip = ({ active, payload }) => {
    if (!active || !payload?.length) return null
    const point = payload[0].payload
    const pointDate = new Date(point.timestamp)
    const todayKey = new Date().toDateString()
    const showDate = pointDate.toDateString() !== todayKey
    return (
      <div style={{ backgroundColor: 'rgba(15, 15, 30, 0.9)', borderRadius: '12px', border: '1px solid rgba(255,255,255,0.1)', backdropFilter: 'blur(10px)', padding: '10px 12px' }}>
        <div style={{ color: '#aaa', marginBottom: '4px' }}>{point.time}</div>
        {showDate && (
          <div style={{ color: '#aaa', marginBottom: '6px' }}>{pointDate.toLocaleDateString([], { year: 'numeric', month: 'short', day: 'numeric' })}</div>
        )}
        <div style={{ color: '#fff', fontWeight: 600 }}>${Number(point.value).toFixed(2)}</div>
      </div>
    )
  }

  return (
    <div className="dashboard-container fade-in">
      <div className="dashboard-header">
        <h2 className="title-gradient">Portfolio Overview</h2>
        <div className="total-balance-badge">
          <span>Total Asset Value</span>
          <h3>${totalValue.toFixed(2)}</h3>
        </div>
      </div>

      <div className="stats-grid">
        <div className="stat-card glass-panel">
          <div className="stat-icon wallet-icon"></div>
          <div className="stat-info">
            <p className="stat-label">Available Balance</p>
            <p className="stat-value">${wallet.balance.toFixed(2)} <span className="currency">{wallet.currency}</span></p>
          </div>
        </div>
        <div className="stat-card glass-panel">
          <div className="stat-icon pos-icon"></div>
          <div className="stat-info">
            <p className="stat-label">Positions Value</p>
            <p className="stat-value">${totalPositionsValue.toFixed(2)} <span className="currency">USDT</span></p>
          </div>
        </div>
        <div className="stat-card glass-panel highlight-card">
          <div className="stat-icon pnl-icon"></div>
          <div className="stat-info">
            <p className="stat-label">Unrealized P&L</p>
            <p className={`stat-value ${totalPnL >= 0 ? 'positive-glow' : 'negative-glow'}`}>
              {totalPnL >= 0 ? '+' : ''}{totalPnL.toFixed(2)} USDT
            </p>
          </div>
        </div>
      </div>

      <div className="chart-section glass-panel">
        <div className="chart-header">
          <h3>Total Value Evolution</h3>
          <div className="chart-controls">
            <span className="live-indicator">● LIVE</span>
            <CustomSelect
              className="compact-select"
              value={selectedPeriod}
              onChange={setSelectedPeriod}
              options={PERIOD_OPTIONS}
            />
          </div>
        </div>
        <div className="chart-container" style={{ height: 300 }}>
          {chartData.length > 0 ? (
            <ResponsiveContainer width="100%" height="100%">
              <AreaChart data={chartData} margin={{ top: 10, right: 30, left: 0, bottom: 0 }}>
                <defs>
                  <linearGradient id="colorValue" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="5%" stopColor="#00f2fe" stopOpacity={0.4}/>
                    <stop offset="95%" stopColor="#4facfe" stopOpacity={0}/>
                  </linearGradient>
                </defs>
                <CartesianGrid strokeDasharray="3 3" stroke="#2a2a4a" vertical={false} />
                <XAxis dataKey="time" stroke="#7e7e9e" tick={{fill: '#7e7e9e'}} axisLine={false} tickLine={false} />
                <YAxis stroke="#7e7e9e" tick={{fill: '#7e7e9e'}} domain={['auto', 'auto']} axisLine={false} tickLine={false} tickFormatter={(value) => `$${value}`} />
                <Tooltip content={<CustomTooltip />} />
                <Area type="monotone" dataKey="value" stroke="#00f2fe" strokeWidth={3} fillOpacity={1} fill="url(#colorValue)" activeDot={{ r: 6, strokeWidth: 0, fill: '#fff' }} />
              </AreaChart>
            </ResponsiveContainer>
          ) : (
             <div className="empty-chart">Waiting for price updates to build history...</div>
          )}
        </div>
      </div>

      <div className="bottom-grid">
        <div className="positions-preview glass-panel">
          <h3>Active Positions <span className="badge">{openPositions.length}</span></h3>
          {openPositions.length === 0 ? (
            <p className="no-data">No active trades right now.</p>
          ) : (
            <div className="table-responsive">
              <table className="modern-table">
                <thead>
                  <tr>
                    <th>Symbol</th>
                    <th>Size</th>
                    <th>Entry</th>
                    <th>Current</th>
                    <th>P&L</th>
                  </tr>
                </thead>
                <tbody>
                  {openPositions.slice(0, 5).map(p => (
                    <tr key={p.id} className="table-row-hover">
                      <td className="font-bold">{p.symbol}</td>
                      <td>{p.amount}</td>
                      <td className="text-muted">${p.avg_price?.toFixed(4)}</td>
                      <td>${p.current_price?.toFixed(4)}</td>
                      <td className={p.pnl >= 0 ? 'positive font-bold' : 'negative font-bold'}>
                        {p.pnl >= 0 ? '+' : ''}{p.pnl?.toFixed(2)} <span className="text-xs">{(p.pnl_percent || 0).toFixed(2)}%</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {analysis && (
          <div className="analysis-card glass-panel">
             <div className="analysis-header">
              <h3>AI Market Scanner</h3>
            </div>
            <div className="scanner-layout">
              <div className="scanner-main flex-col">
                <div className="selected-asset">
                  <span className="asset-name">{selectedSymbol}</span>
                  <span className="asset-price">${analysis.current_price}</span>
                </div>
                <div className={`signal-badge pulse signal-${analysis.final_signal?.toLowerCase()}`}>
                  {analysis.final_signal?.replace('_', ' ')}
                </div>
                
                <div className="indicators-mini-grid mt-4">
                  {analysis.indicators?.slice(0,4).map((ind, idx) => (
                    <div key={`${ind.name}-${idx}`} className="mini-indicator">
                      <span className="ind-name">{ind.name}</span>
                      <span className={`ind-value color-${ind.signal?.toLowerCase()}`}>{ind.value?.toString().substring(0,5)}</span>
                    </div>
                  ))}
                </div>
              </div>
              <div className="scanner-sidebar flex-col">
                <p className="sidebar-title">Hot Assets</p>
                <div className="coin-list">
                  {recentCoins.slice(0, 5).map((coin, idx) => (
                    <button 
                      key={`${coin.symbol}-${idx}`}
                      type="button"
                      className={`hot-coin-btn ${coin.symbol === selectedSymbol ? 'active' : ''}`}
                      onClick={() => setSelectedSymbol(coin.symbol)}
                    >
                      <span className="coin-name">{coin.symbol.replace('/USDT', '')}</span>
                      <span className={`coin-dot color-${coin.signal?.toLowerCase()}`}>●</span>
                    </button>
                  ))}
                </div>
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

export default Dashboard
