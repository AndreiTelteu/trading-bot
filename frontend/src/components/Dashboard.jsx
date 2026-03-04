import React, { useState, useEffect, useCallback, useMemo } from 'react'
import { XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, AreaChart, Area } from 'recharts'
import { useWebSocketEvent } from '../hooks/useWebSocket'
import CustomSelect from './CustomSelect'
import Modal from './Modal'

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

const backfillMissingHours = (data, periodMs) => {
  if (!Array.isArray(data)) {
    return []
  }
  const now = Date.now()
  const startTime = now - periodMs
  const hourKey = (timeMs) => {
    const rounded = new Date(timeMs)
    rounded.setMinutes(0, 0, 0)
    return rounded.getTime()
  }
  const withinRange = data.filter((snapshot) => {
    const time = new Date(snapshot.timestamp).getTime()
    return !Number.isNaN(time) && time >= startTime && time <= now
  })
  const hoursWithData = new Set()
  for (const snapshot of withinRange) {
    const time = new Date(snapshot.timestamp).getTime()
    if (!Number.isNaN(time)) {
      hoursWithData.add(hourKey(time))
    }
  }
  const placeholders = []
  for (let t = hourKey(startTime); t <= hourKey(now); t += 60 * 60 * 1000) {
    if (!hoursWithData.has(t)) {
      placeholders.push({
        timestamp: new Date(t).toISOString(),
        total_value: null,
      })
    }
  }
  return [...withinRange, ...placeholders].sort((a, b) => new Date(a.timestamp) - new Date(b.timestamp))
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
    const currentValue = snapshot.total_value === null || snapshot.total_value === undefined ? null : Number(snapshot.total_value)
    const existing = buckets.get(bucket)
    if (currentValue === null) {
      if (!existing) {
        buckets.set(bucket, {
          timestamp: snapshot.timestamp,
          total_value: null,
        })
      }
    } else {
      if (!existing || existing.total_value === null || currentValue > existing.total_value) {
        buckets.set(bucket, {
          timestamp: snapshot.timestamp,
          total_value: currentValue,
        })
      }
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
  const [modalData, setModalData] = useState(null)
  const [loadingModal, setLoadingModal] = useState(false)
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
      }
    } catch (err) {}
  }, [])

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
    if (!selectedSymbol) return
    
    const fetchDetails = async () => {
      setLoadingModal(true)
      try {
        const res = await fetch(`${API_BASE}/analysis/history/${selectedSymbol}`)
        if (res.ok) {
          const data = await res.json()
          setModalData(data)
        }
      } catch (err) {
        console.error(err)
      } finally {
        setLoadingModal(false)
      }
    }
    fetchDetails()
  }, [selectedSymbol])

  useEffect(() => {
    fetchSnapshots()
  }, [fetchSnapshots])

  // Listen for WebSocket updates - Activity Logs for live scanner
  useWebSocketEvent('activity_log_new', useCallback((log) => {
    if (log.log_type === 'system' && log.message.includes('Starting trending coins analysis')) {
      // Optional: Clear list when new analysis starts to show fresh results
      setRecentCoins([]) 
    } else if (log.log_type === 'analysis' && log.message.startsWith('Analyzed ')) {
      const symbol = log.message.replace('Analyzed ', '')
      let signal = 'NEUTRAL'
      
      // Parse signal from details (e.g., "Signal: BUY, Rating: 4.50")
      if (log.details) {
        const signalMatch = log.details.match(/Signal:\s*([A-Z_]+)/)
        if (signalMatch) {
          signal = signalMatch[1]
        }
      }

      setRecentCoins(prev => {
        // Remove existing entry for this symbol if any, then add new one
        const filtered = prev.filter(c => c.symbol !== symbol)
        return [...filtered, { symbol, signal }]
      })
    }
  }, []))

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

  // Keep trending_update as a fallback/sync, but prioritize activity logs for "live" feel
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

  // Format data for Recharts
  const periodMs = periodToMs(selectedPeriod)
  const maxChartPoints = periodMs >= 24 * 60 * 60 * 1000 ? 120 : 80
  const filledSnapshots = useMemo(
    () => backfillMissingHours(snapshots, periodMs),
    [snapshots, periodMs]
  )
  const aggregatedSnapshots = useMemo(
    () => aggregateSnapshots(filledSnapshots, maxChartPoints),
    [filledSnapshots, maxChartPoints]
  )
  const chartData = aggregatedSnapshots.map(s => ({
    time: new Date(s.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    value: s.total_value === null || s.total_value === undefined ? null : Number(s.total_value),
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
        <div style={{ color: '#fff', fontWeight: 600 }}>{point.value === null ? 'No data' : `$${Number(point.value).toFixed(2)}`}</div>
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

        <div className="analysis-card glass-panel" style={{ width: '100%' }}>
           <div className="analysis-header">
            <h3>AI Market Scanner</h3>
            <span className="live-indicator">● Live Activity</span>
          </div>
          <div className="coin-list-flex" style={{ display: 'flex', flexWrap: 'wrap', gap: '10px', marginTop: '15px' }}>
            {recentCoins.map((coin, idx) => (
              <button 
                key={`${coin.symbol}-${idx}`}
                type="button"
                className={`hot-coin-btn ${coin.symbol === selectedSymbol ? 'active' : ''}`}
                style={{ 
                  display: 'flex', 
                  alignItems: 'center', 
                  gap: '8px',
                  padding: '8px 12px',
                  background: coin.symbol === selectedSymbol ? 'rgba(255,255,255,0.15)' : 'rgba(255,255,255,0.05)',
                  border: coin.symbol === selectedSymbol ? '1px solid rgba(255,255,255,0.3)' : '1px solid rgba(255,255,255,0.1)',
                  borderRadius: '6px',
                  cursor: 'pointer',
                  color: '#fff',
                  transition: 'all 0.2s'
                }}
                onClick={() => setSelectedSymbol(coin.symbol)}
              >
                <span style={{ fontWeight: 600 }}>{coin.symbol.replace('/USDT', '').replace('USDT', '')}</span>
                <span style={{ 
                  color: SIGNAL_COLORS[coin.signal] || '#888',
                  fontSize: '1.2em',
                  lineHeight: 1
                }}>●</span>
              </button>
            ))}
            {recentCoins.length === 0 && <p className="text-muted" style={{ width: '100%', fontStyle: 'italic' }}>Waiting for live market activity...</p>}
          </div>
        </div>
      </div>
      
      <Modal
        isOpen={Boolean(selectedSymbol)}
        onClose={() => setSelectedSymbol(null)}
        overlayStyle={{ display: 'flex', alignItems: 'center', justifyContent: 'center', position: 'fixed', top: 0, left: 0, right: 0, bottom: 0, backgroundColor: 'rgba(0,0,0,0.7)', zIndex: 1000 }}
        panelStyle={{ width: '90%', maxWidth: '600px', maxHeight: '90vh', overflowY: 'auto', padding: '2rem', position: 'relative' }}
      >
        <button className="modal-close" onClick={() => setSelectedSymbol(null)} style={{ position: 'absolute', top: '1rem', right: '1rem', background: 'none', border: 'none', color: '#fff', fontSize: '1.5rem', cursor: 'pointer' }}>×</button>
        
        <h2 className="title-gradient mb-4">{selectedSymbol} Analysis</h2>
        
        {loadingModal ? (
          <div className="flex-center" style={{ padding: '2rem', display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
            <div className="loading-spinner" style={{ border: '3px solid rgba(255,255,255,0.1)', borderTop: '3px solid #00f2fe', borderRadius: '50%', width: '40px', height: '40px', animation: 'spin 1s linear infinite', marginBottom: '1rem' }}></div>
            <p>Fetching detailed analysis...</p>
            <style>{`@keyframes spin { 0% { transform: rotate(0deg); } 100% { transform: rotate(360deg); } }`}</style>
          </div>
        ) : modalData ? (
          <div className="analysis-details">
            <div className="flex-between mb-4" style={{ display: 'flex', justifyContent: 'space-between' }}>
              <div>
                <span className="text-muted block" style={{ display: 'block', marginBottom: '0.25rem' }}>Current Price</span>
                <span className="text-xl font-bold" style={{ fontSize: '1.5rem', fontWeight: 'bold' }}>${modalData.price}</span>
              </div>
              <div>
                <span className="text-muted block" style={{ display: 'block', marginBottom: '0.25rem' }}>24h Change</span>
                <span className={`text-xl font-bold ${modalData.change_24h >= 0 ? 'positive' : 'negative'}`} style={{ fontSize: '1.5rem', fontWeight: 'bold', color: modalData.change_24h >= 0 ? '#52b788' : '#e63946' }}>
                  {modalData.change_24h}%
                </span>
              </div>
            </div>

            <div className="signal-box p-4 rounded bg-dark-glass mb-4 border border-glass" style={{ background: 'rgba(0,0,0,0.2)', padding: '1rem', borderRadius: '8px', border: '1px solid rgba(255,255,255,0.1)', marginBottom: '1rem' }}>
               <div className="flex-between mb-2" style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
                 <span className="text-muted">Signal</span>
                 <span className={`signal-badge signal-${modalData.signal?.toLowerCase()}`}>{modalData.signal?.replace('_', ' ')}</span>
               </div>
               <div className="flex-between" style={{ display: 'flex', justifyContent: 'space-between' }}>
                 <span className="text-muted">Confidence Rating</span>
                 <div className="rating-bar">
                   <span style={{ fontWeight: 'bold', color: modalData.rating >= 7 ? '#52b788' : modalData.rating >= 4 ? '#e9c46a' : '#e76f51' }}>
                     {modalData.rating}/10
                   </span>
                 </div>
               </div>
            </div>

            {modalData.decision && (
              <div className="decision-box p-4 rounded bg-dark-glass mb-4 border border-glass" style={{ background: 'rgba(0,0,0,0.2)', padding: '1rem', borderRadius: '8px', border: '1px solid rgba(255,255,255,0.1)', marginBottom: '1rem' }}>
                <h4 className="text-muted mb-2 uppercase text-xs" style={{ textTransform: 'uppercase', fontSize: '0.75rem', marginBottom: '0.5rem' }}>AI Decision</h4>
                <div className="flex items-center gap-2 mb-2">
                   <span className={`font-bold ${modalData.decision === 'buy' ? 'positive' : 'text-muted'}`} style={{ color: modalData.decision === 'buy' ? '#52b788' : '#aaa' }}>
                     {modalData.decision.toUpperCase()}
                   </span>
                </div>
                <p className="text-sm italic opacity-80">{modalData.decision_reason}</p>
              </div>
            )}

            <h4 className="text-muted mb-3 uppercase text-xs mt-6" style={{ marginTop: '1.5rem', marginBottom: '0.75rem', textTransform: 'uppercase', fontSize: '0.75rem' }}>Technical Indicators</h4>
            <div className="indicators-grid grid grid-cols-2 gap-3" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.75rem' }}>
              {modalData.indicators?.map((ind, i) => (
                <div key={i} className="indicator-card p-3 rounded bg-white-5" style={{ background: 'rgba(255,255,255,0.05)', padding: '0.75rem', borderRadius: '8px' }}>
                  <div className="flex-between mb-1" style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '0.25rem' }}>
                    <span className="font-bold text-sm">{ind.name}</span>
                    <span className={`text-xs signal-${ind.signal?.toLowerCase()}`} style={{ fontSize: '0.75rem' }}>{ind.signal}</span>
                  </div>
                  <div className="text-xs opacity-70 truncate" style={{ fontSize: '0.75rem', opacity: 0.7, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{ind.value}</div>
                </div>
              ))}
            </div>
          </div>
        ) : (
           <p>No details available.</p>
        )}
      </Modal>
    </div>
  )
}

export default Dashboard
