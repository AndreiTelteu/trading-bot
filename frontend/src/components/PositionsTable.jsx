import React, { useState } from 'react'

const API_BASE = '/api'

function PositionsTable({ positions, onRefresh }) {
  const [symbol, setSymbol] = useState('')
  const [amount, setAmount] = useState('')
  const [avgPrice, setAvgPrice] = useState('')
  const [loading, setLoading] = useState(false)

  const handleAddPosition = async (e) => {
    e.preventDefault()
    if (!symbol || !amount || !avgPrice) return
    
    setLoading(true)
    try {
      await fetch(`${API_BASE}/positions`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          symbol: symbol.toUpperCase(),
          amount: parseFloat(amount),
          avg_price: parseFloat(avgPrice),
          current_price: parseFloat(avgPrice)
        })
      })
      setSymbol('')
      setAmount('')
      setAvgPrice('')
      onRefresh()
    } catch (err) {
      console.error('Failed to add position:', err)
    }
    setLoading(false)
  }

  const handleClosePosition = async (positionId) => {
    if (!confirm('Close this position?')) return
    
    try {
      await fetch(`${API_BASE}/positions/${positionId}/close`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason: 'manual' })
      })
      onRefresh()
    } catch (err) {
      console.error('Failed to close position:', err)
    }
  }

  const openPositions = positions.filter(p => p.status === 'open')
  const closedPositions = positions.filter(p => p.status === 'closed')

  return (
    <div className="positions-container fade-in">
      <div className="dashboard-header">
        <h2 className="title-gradient">Positions Engine</h2>
        <div className="total-balance-badge">
          <span>Active Trades</span>
          <h3>{openPositions.length}</h3>
        </div>
      </div>
      
      <div className="glass-panel mb-4">
        <h3 className="mb-2 uppercase text-muted tracking-wide text-sm font-bold">Add Manual Position</h3>
        <form className="add-position-form flex gap-3" onSubmit={handleAddPosition}>
          <input
            type="text"
            className="form-input flex-1"
            placeholder="Asset Symbol (e.g. BTCUSDT)"
            value={symbol}
            onChange={e => setSymbol(e.target.value)}
          />
          <input
            type="number"
            className="form-input flex-1"
            placeholder="Size / Amount"
            value={amount}
            onChange={e => setAmount(e.target.value)}
            step="any"
          />
          <input
            type="number"
            className="form-input flex-1"
            placeholder="Entry Price"
            value={avgPrice}
            onChange={e => setAvgPrice(e.target.value)}
            step="any"
          />
          <button type="submit" className="btn-primary" disabled={loading}>
            {loading ? 'Executing...' : 'Open Position'}
          </button>
        </form>
      </div>

      <div className="positions-section glass-panel mb-4">
        <h3 className="mb-3 uppercase text-muted tracking-wide text-sm font-bold">Open Positions</h3>
        {openPositions.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon text-4xl mb-2 block">📊</span>
            <p className="no-data">No open positions. Waiting for signals...</p>
          </div>
        ) : (
          <div className="table-responsive">
            <table className="modern-table">
              <thead>
                <tr>
                  <th>Asset</th>
                  <th>Position Size</th>
                  <th>Entry Price</th>
                  <th>Current Price</th>
                  <th>Unrealized P&L</th>
                  <th>Net ROI</th>
                  <th>Actions</th>
                </tr>
              </thead>
              <tbody>
                {openPositions.map(p => (
                  <tr key={p.id} className="table-row-hover">
                    <td className="font-bold text-lg">{p.symbol}</td>
                    <td>{p.amount}</td>
                    <td className="text-muted">${p.avg_price?.toFixed(4)}</td>
                    <td>${p.current_price?.toFixed(4)}</td>
                    <td className={p.pnl >= 0 ? 'positive-glow font-bold' : 'negative-glow font-bold'}>
                      {p.pnl >= 0 ? '+' : ''}{p.pnl?.toFixed(2)} USDT
                    </td>
                    <td>
                      <span className={`signal-badge ${p.pnl_percent >= 0 ? 'signal-strong_buy' : 'signal-strong_sell'}`} style={ {padding: '0.4rem 0.8rem', fontSize: '0.8rem'} }>
                        {p.pnl_percent >= 0 ? '+' : ''}{(p.pnl_percent || 0).toFixed(2)}%
                      </span>
                    </td>
                    <td>
                      <button 
                        className="btn-danger"
                        onClick={() => handleClosePosition(p.id)}
                      >
                        Close Trade
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {closedPositions.length > 0 && (
        <div className="positions-section glass-panel">
          <h3 className="mb-3 uppercase text-muted tracking-wide text-sm font-bold">Trade History (Closed)</h3>
          <div className="table-responsive">
            <table className="modern-table opacity-80">
              <thead>
                <tr>
                  <th>Asset</th>
                  <th>Size</th>
                  <th>Entry</th>
                  <th>Exit Price</th>
                  <th>Realized P&L</th>
                  <th>Trigger</th>
                  <th>Time Closed</th>
                </tr>
              </thead>
              <tbody>
                {closedPositions.map(p => (
                  <tr key={p.id}>
                    <td className="font-bold">{p.symbol}</td>
                    <td>{p.amount}</td>
                    <td className="text-muted">${p.avg_price?.toFixed(4)}</td>
                    <td>${p.current_price?.toFixed(4)}</td>
                    <td className={p.pnl >= 0 ? 'positive font-bold' : 'negative font-bold'}>
                      {p.pnl >= 0 ? '+' : ''}{p.pnl?.toFixed(2)} USDT
                    </td>
                    <td>
                       <span className={`badge-type badge-${p.close_reason === 'take_profit' ? 'trade' : p.close_reason === 'stop_loss' ? 'error' : 'system'}`}>
                         {p.close_reason?.toUpperCase() || 'MANUAL'}
                       </span>
                    </td>
                    <td className="text-xs text-muted">{p.closed_at ? new Date(p.closed_at).toLocaleString() : '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}

export default PositionsTable
