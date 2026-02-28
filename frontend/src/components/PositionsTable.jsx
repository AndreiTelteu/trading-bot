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

  const handleDeletePosition = async (symbol) => {
    if (!confirm(`Delete position ${symbol}?`)) return
    
    try {
      await fetch(`${API_BASE}/positions/${symbol}`, {
        method: 'DELETE'
      })
      onRefresh()
    } catch (err) {
      console.error('Failed to delete position:', err)
    }
  }

  const openPositions = positions.filter(p => p.status === 'open')
  const closedPositions = positions.filter(p => p.status === 'closed')

  return (
    <div className="positions-table">
      <h2>Positions</h2>
      
      <form className="add-position-form" onSubmit={handleAddPosition}>
        <h3>Add Position</h3>
        <div className="form-row">
          <input
            type="text"
            placeholder="Symbol (e.g. BTC)"
            value={symbol}
            onChange={e => setSymbol(e.target.value)}
          />
          <input
            type="number"
            placeholder="Amount"
            value={amount}
            onChange={e => setAmount(e.target.value)}
            step="any"
          />
          <input
            type="number"
            placeholder="Avg Price"
            value={avgPrice}
            onChange={e => setAvgPrice(e.target.value)}
            step="any"
          />
          <button type="submit" disabled={loading}>
            {loading ? 'Adding...' : 'Add'}
          </button>
        </div>
      </form>

      <div className="positions-section">
        <h3>Open Positions ({openPositions.length})</h3>
        {openPositions.length === 0 ? (
          <p className="no-data">No open positions</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Symbol</th>
                <th>Amount</th>
                <th>Avg Price</th>
                <th>Current Price</th>
                <th>P&L</th>
                <th>P&L %</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {openPositions.map(p => (
                <tr key={p.id}>
                  <td>{p.symbol}</td>
                  <td>{p.amount}</td>
                  <td>${p.avg_price}</td>
                  <td>${p.current_price}</td>
                  <td className={p.pnl >= 0 ? 'positive' : 'negative'}>
                    {p.pnl >= 0 ? '+' : ''}{p.pnl}
                  </td>
                  <td className={p.pnl_percent >= 0 ? 'positive' : 'negative'}>
                    {p.pnl_percent >= 0 ? '+' : ''}{p.pnl_percent}%
                  </td>
                  <td>
                    <button 
                      className="btn-close"
                      onClick={() => handleClosePosition(p.id)}
                    >
                      Close
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {closedPositions.length > 0 && (
        <div className="positions-section">
          <h3>Closed Positions ({closedPositions.length})</h3>
          <table>
            <thead>
              <tr>
                <th>Symbol</th>
                <th>Amount</th>
                <th>Avg Price</th>
                <th>Close Price</th>
                <th>P&L</th>
                <th>Reason</th>
                <th>Closed</th>
              </tr>
            </thead>
            <tbody>
              {closedPositions.map(p => (
                <tr key={p.id}>
                  <td>{p.symbol}</td>
                  <td>{p.amount}</td>
                  <td>${p.avg_price}</td>
                  <td>${p.current_price}</td>
                  <td className={p.pnl >= 0 ? 'positive' : 'negative'}>
                    {p.pnl >= 0 ? '+' : ''}{p.pnl}
                  </td>
                  <td>{p.close_reason}</td>
                  <td>{p.closed_at ? new Date(p.closed_at).toLocaleDateString() : '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

export default PositionsTable
