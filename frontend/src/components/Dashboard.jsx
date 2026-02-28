import React, { useState, useEffect } from 'react'
import { LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, AreaChart, Area } from 'recharts'

const API_BASE = '/api'

const SIGNAL_COLORS = {
  STRONG_BUY: '#52b788',
  BUY: '#95d5b2',
  NEUTRAL: '#888',
  SELL: '#f4978e',
  STRONG_SELL: '#e63946',
}

function Dashboard({ wallet: propWallet, positions: propPositions }) {
  const [orders, setOrders] = useState([])
  const [recentCoins, setRecentCoins] = useState([])
  const [selectedSymbol, setSelectedSymbol] = useState(null)
  const [snapshots, setSnapshots] = useState([])
  const [wallet, setWallet] = useState(propWallet || { balance: 0, currency: 'USDT' })
  const [positions, setPositions] = useState(propPositions || [])

  useEffect(() => {
    fetchOrders()
    fetchRecentCoins()
    fetchWallet()
    fetchPositions()
    fetchSnapshots()
    const interval = setInterval(fetchRecentCoins, 30000)
    const walletInterval = setInterval(() => { fetchWallet(); fetchPositions(); fetchSnapshots() }, 10000)
    const ordersInterval = setInterval(fetchOrders, 10000)
    return () => {
      clearInterval(interval)
      clearInterval(walletInterval)
      clearInterval(ordersInterval)
    }
  }, [selectedSymbol])

  const fetchRecentCoins = async () => {
    try {
      const res = await fetch(`${API_BASE}/trending/recent`)
      if (res.ok) {
        const data = await res.json()
        const coins = data.coins || []
        setRecentCoins(coins)
        if (coins.length > 0 && selectedSymbol === null) setSelectedSymbol(coins[0].symbol)
      }
    } catch (err) {}
  }

  const fetchSnapshots = async () => {
    try {
      const res = await fetch(`${API_BASE}/wallet/snapshots`)
      if (res.ok) {
        const data = await res.json()
        setSnapshots(data)
      }
    } catch (err) {}
  }

  const fetchOrders = async () => {
    try {
      const res = await fetch(`${API_BASE}/orders?limit=10`)
      if (res.ok) setOrders(await res.json())
    } catch (err) {}
  }

  const fetchWallet = async () => {
    try {
      const res = await fetch(`${API_BASE}/wallet`)
      if (res.ok) setWallet(await res.json())
    } catch (err) {}
  }

  const fetchPositions = async () => {
    try {
      const res = await fetch(`${API_BASE}/positions`)
      if (res.ok) setPositions(await res.json())
    } catch (err) {}
  }

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
  const chartData = snapshots.map(s => ({
    time: new Date(s.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }),
    value: s.total_value.toFixed(2),
  }))

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
          <span className="live-indicator">● LIVE</span>
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
                <Tooltip 
                  contentStyle={{ backgroundColor: 'rgba(15, 15, 30, 0.9)', borderRadius: '12px', border: '1px solid rgba(255,255,255,0.1)', backdropFilter: 'blur(10px)' }}
                  itemStyle={{ color: '#fff', fontWeight: 600 }}
                  labelStyle={{ color: '#aaa', marginBottom: '5px' }}
                />
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
                  <span className="asset-price">${Date.now() % 2 === 0 ? analysis.current_price : analysis.current_price}</span>
                </div>
                <div className={`signal-badge pulse signal-${analysis.final_signal?.toLowerCase()}`}>
                  {analysis.final_signal?.replace('_', ' ')}
                </div>
                
                <div className="indicators-mini-grid mt-4">
                  {analysis.indicators?.slice(0,4).map((ind, i) => (
                    <div key={i} className="mini-indicator">
                      <span className="ind-name">{ind.name}</span>
                      <span className={`ind-value color-${ind.signal?.toLowerCase()}`}>{ind.value?.toString().substring(0,5)}</span>
                    </div>
                  ))}
                </div>
              </div>
              <div className="scanner-sidebar flex-col">
                <p className="sidebar-title">Hot Assets</p>
                <div className="coin-list">
                  {recentCoins.slice(0, 5).map((coin, i) => (
                    <button 
                      key={i} 
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
