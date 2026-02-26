import React, { useState, useEffect } from 'react'
import { LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts'

const API_BASE = '/api'

const SIGNAL_COLORS = {
  STRONG_BUY: '#52b788',
  BUY: '#95d5b2',
  NEUTRAL: '#666',
  SELL: '#f4978e',
  STRONG_SELL: '#e63946',
}

function Dashboard({ wallet: propWallet, positions: propPositions }) {
  const [analysis, setAnalysis] = useState(null)
  const [orders, setOrders] = useState([])
  const [recentCoins, setRecentCoins] = useState([])
  const [selectedSymbol, setSelectedSymbol] = useState(null)
  const [loading, setLoading] = useState(false)
  const [wallet, setWallet] = useState(propWallet || { balance: 0, currency: 'USDT' })
  const [positions, setPositions] = useState(propPositions || [])

  useEffect(() => {
    fetchAnalysis(selectedSymbol)
    fetchOrders()
    fetchRecentCoins()
    fetchWallet()
    fetchPositions()
    const interval = setInterval(fetchRecentCoins, 30000)
    const analysisInterval = setInterval(() => fetchAnalysis(selectedSymbol), 30000)
    const walletInterval = setInterval(fetchWallet, 10000)
    const positionsInterval = setInterval(fetchPositions, 10000)
    const ordersInterval = setInterval(fetchOrders, 10000)
    return () => {
      clearInterval(interval)
      clearInterval(analysisInterval)
      clearInterval(walletInterval)
      clearInterval(positionsInterval)
      clearInterval(ordersInterval)
    }
  }, [selectedSymbol])

  const fetchAnalysis = async (symbol) => {
    if (!symbol) return
    try {
      const res = await fetch(`${API_BASE}/analysis?symbol=${encodeURIComponent(symbol)}`)
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`)
      }
      const data = await res.json()
      setAnalysis(data)
    } catch (err) {
      console.error('Failed to fetch analysis:', err)
    }
  }

  const fetchRecentCoins = async () => {
    try {
      const res = await fetch(`${API_BASE}/trending/recent`)
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`)
      }
      const data = await res.json()
      const coins = data.coins || []
      setRecentCoins(coins)
      if (coins.length > 0 && selectedSymbol === null) {
        setSelectedSymbol(coins[0].symbol)
      }
    } catch (err) {
      console.error('Failed to fetch recent coins:', err)
    }
  }

  const handleCoinClick = (symbol) => {
    setSelectedSymbol(symbol)
    const cachedCoin = recentCoins.find(c => c.symbol === symbol)
    if (cachedCoin) {
      setAnalysis({
        current_price: cachedCoin.price,
        final_signal: cachedCoin.signal,
        rating: cachedCoin.rating,
        change_24h: cachedCoin.change_24h,
        timestamp: new Date().toISOString(),
        indicators: [],
        fromCache: true
      })
    }
  }

  const handleRefreshAnalysis = async (symbol) => {
    setLoading(true)
    await fetchAnalysis(symbol)
    setLoading(false)
  }

  const fetchOrders = async () => {
    try {
      const res = await fetch(`${API_BASE}/orders?limit=10`)
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`)
      }
      const data = await res.json()
      setOrders(data)
    } catch (err) {
      console.error('Failed to fetch orders:', err)
    }
  }

  const fetchWallet = async () => {
    try {
      const res = await fetch(`${API_BASE}/wallet`)
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`)
      }
      const data = await res.json()
      setWallet(data)
    } catch (err) {
      console.error('Failed to fetch wallet:', err)
    }
  }

  const fetchPositions = async () => {
    try {
      const res = await fetch(`${API_BASE}/positions`)
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`)
      }
      const data = await res.json()
      setPositions(data)
    } catch (err) {
      console.error('Failed to fetch positions:', err)
    }
  }

  const totalPositionsValue = positions.reduce((sum, p) => {
    return sum + (p.current_price || 0) * p.amount
  }, 0)

  const totalPnL = positions.reduce((sum, p) => sum + (p.pnl || 0), 0)

  return (
    <div className="dashboard">
      <div className="stats-grid">
        <div className="stat-card">
          <h3>Balance</h3>
          <p className="stat-value">${wallet.balance?.toFixed(2)}</p>
          <p className="stat-label">{wallet.currency}</p>
        </div>
        <div className="stat-card">
          <h3>Positions Value</h3>
          <p className="stat-value">${totalPositionsValue.toFixed(2)}</p>
          <p className="stat-label">USDT</p>
        </div>
        <div className="stat-card">
          <h3>Total Value</h3>
          <p className="stat-value">${(wallet.balance + totalPositionsValue).toFixed(2)}</p>
          <p className="stat-label">USDT</p>
        </div>
        <div className="stat-card">
          <h3>Total P&L</h3>
          <p className={`stat-value ${totalPnL >= 0 ? 'positive' : 'negative'}`}>
            {totalPnL >= 0 ? '+' : ''}{totalPnL.toFixed(2)} USDT
          </p>
        </div>
      </div>

      <div className="positions-preview">
        <h3>Open Positions</h3>
        {positions.length === 0 ? (
          <p className="no-data">No open positions</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Symbol</th>
                <th>Amount</th>
                <th>Avg Price</th>
                <th>Current</th>
                <th>P&L</th>
              </tr>
            </thead>
            <tbody>
              {positions.slice(0, 5).map(p => (
                <tr key={p.id}>
                  <td>{p.symbol}</td>
                  <td>{p.amount.toFixed(4)}</td>
                  <td>${p.avg_price?.toFixed(2)}</td>
                  <td>${p.current_price?.toFixed(2)}</td>
                  <td className={p.pnl >= 0 ? 'positive' : 'negative'}>
                    {p.pnl >= 0 ? '+' : ''}{p.pnl?.toFixed(2)} ({p.pnl_percent?.toFixed(1)}%)
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="orders-preview">
        <h3>Recent Orders</h3>
        {orders.length === 0 ? (
          <p className="no-data">No orders yet</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Type</th>
                <th>Symbol</th>
                <th>Amount</th>
                <th>Price</th>
                <th>Time</th>
              </tr>
            </thead>
            <tbody>
              {orders.map(o => (
                <tr key={o.id}>
                  <td className={o.order_type}>{o.order_type.toUpperCase()}</td>
                  <td>{o.symbol}</td>
                  <td>{o.amount_crypto.toFixed(4)}</td>
                  <td>${o.price?.toFixed(2)}</td>
                  <td>{new Date(o.executed_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {loading ? (
        <div className="analysis-card loading">
          <p>Loading analysis...</p>
        </div>
      ) : analysis ? (
        <div className="analysis-card">
          <div className="analysis-header">
            <h2>Market Analysis: {selectedSymbol}</h2>
            <button 
              className="refresh-btn" 
              onClick={() => handleRefreshAnalysis(selectedSymbol)}
              disabled={loading}
            >
              {loading ? 'Refreshing...' : 'Refresh'}
            </button>
          </div>
          {analysis.fromCache && (
            <p className="cached-notice">Showing cached data from recent coins</p>
          )}
          <div className="analysis-content">
            <div className="analysis-main">
              <div className="analysis-price">
                <span className="price">${analysis.current_price?.toFixed(2)}</span>
                <span className={`signal ${analysis.final_signal?.toLowerCase()}`}>
                  {analysis.final_signal}
                </span>
              </div>
              <p className="analysis-time">Last update: {new Date(analysis.timestamp).toLocaleString()}</p>
              
              <div className="indicators-grid">
                {analysis.indicators?.map((ind, i) => (
                  <div key={i} className={`indicator ${ind.signal}`}>
                    <h4>{ind.name}</h4>
                    <p className="indicator-value">{ind.value}</p>
                    <span className="indicator-signal">{ind.signal.toUpperCase()}</span>
                  </div>
                ))}
              </div>
            </div>
            <div className="recent-coins-sidebar">
              <h3>Recent</h3>
              {recentCoins.length === 0 ? (
                <p className="no-data">No recent analysis</p>
              ) : (
                <div className="recent-coins-badges">
                  {recentCoins.slice(0, 5).map((coin, i) => (
                    <div 
                      key={i} 
                      className={`coin-badge coin-badge-${coin.signal?.toLowerCase()} ${coin.symbol === selectedSymbol ? 'active' : ''}`}
                      onClick={() => handleCoinClick(coin.symbol)}
                    >
                      <span className="coin-badge-name">{coin.symbol.replace('/USDT', '')}</span>
                      <span className="coin-badge-signal">{coin.signal}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>
      ) : null}
    </div>
  )
}

export default Dashboard
