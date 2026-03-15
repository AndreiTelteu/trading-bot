import React, { useState } from 'react'
import AlertDialog from './AlertDialog'
import useAlertDialog from '../hooks/useAlertDialog'

const API_BASE = '/api'

function PositionsTable({ positions, onRefresh }) {
  const [symbol, setSymbol] = useState('')
  const [amount, setAmount] = useState('')
  const [price, setPrice] = useState('')
  const [orderType, setOrderType] = useState('market')
  const [loading, setLoading] = useState(false)
  const [tradeResult, setTradeResult] = useState(null)
  const [error, setError] = useState(null)
  const closeTradeDialog = useAlertDialog()

  const handleOpenTrade = async (e) => {
    e.preventDefault()
    if (!symbol || !amount) return
    
    setLoading(true)
    setError(null)
    setTradeResult(null)
    
    try {
      const response = await fetch(`${API_BASE}/positions-trade/open`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          symbol: symbol.toUpperCase(),
          amount: parseFloat(amount),
          price: price ? parseFloat(price) : 0,
          order_type: orderType
        })
      })
      
      const data = await response.json()
      
      if (!response.ok) {
        throw new Error(data.error || 'Failed to execute trade')
      }
      
      setTradeResult({
        type: 'success',
        action: 'buy',
        message: `Successfully bought ${data.amount} ${data.symbol} at $${data.price.toFixed(4)}`,
        details: data
      })
      
      setSymbol('')
      setAmount('')
      setPrice('')
      onRefresh()
    } catch (err) {
      console.error('Failed to execute trade:', err)
      setError(err.message)
      setTradeResult({
        type: 'error',
        action: 'buy',
        message: err.message
      })
    }
    setLoading(false)
  }

  const handleCloseTrade = async (positionId, positionSymbol, positionAmount) => {
    setLoading(true)
    setError(null)
    setTradeResult(null)
    
    try {
      const response = await fetch(`${API_BASE}/positions-trade/${positionId}/close`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          close_reason: 'manual',
          price: 0,
          order_type: 'market'
        })
      })
      
      const data = await response.json()
      
      if (!response.ok) {
        throw new Error(data.error || 'Failed to close trade')
      }
      
      const pnlSign = data.pnl >= 0 ? '+' : ''
      const pnlEmoji = data.pnl >= 0 ? '🟢' : '🔴'
      
      setTradeResult({
        type: 'success',
        action: 'sell',
        message: `${pnlEmoji} Sold ${data.amount} ${data.symbol} at $${data.price.toFixed(4)} | P&L: ${pnlSign}${data.pnl.toFixed(2)} USDT (${pnlSign}${data.pnl_percent.toFixed(2)}%)`,
        details: data
      })
      
      onRefresh()
    } catch (err) {
      console.error('Failed to close trade:', err)
      setError(err.message)
      setTradeResult({
        type: 'error',
        action: 'sell',
        message: err.message
      })
    }
    setLoading(false)
  }

  const openCloseTradeDialog = (position) => {
    const currentPrice = Number(position.current_price || 0)
    const pnl = Number(position.pnl || 0)
    const pnlPercent = Number(position.pnl_percent || 0)
    const pnlDescriptor = pnl >= 0 ? 'gain' : 'loss'

    closeTradeDialog.openDialog({
      tone: pnl >= 0 ? 'warning' : 'danger',
      title: `Close ${position.symbol}?`,
      message: `This will execute a market sell for ${position.amount} ${position.symbol} and immediately realize the current ${pnlDescriptor}.`,
      description: `Current price: $${currentPrice.toFixed(4)}. Estimated P&L: ${pnl >= 0 ? '+' : ''}${pnl.toFixed(2)} USDT (${pnl >= 0 ? '+' : ''}${pnlPercent.toFixed(2)}%). This action closes the live trade and moves it to history.`,
      buttons: [
        {
          label: 'Keep Position Open',
          variant: 'ghost',
          closeOnClick: true,
        },
        {
          label: 'Close Trade',
          variant: 'danger',
          autoFocus: true,
          closeOnClick: true,
          onClick: () => handleCloseTrade(position.id, position.symbol, position.amount),
        },
      ],
    })
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
        <h3 className="mb-3 uppercase text-muted tracking-wide text-sm font-bold">Open New Position</h3>
        
        {/* Trade Result Notifications */}
        {tradeResult && (
          <div className={`alert ${tradeResult.type === 'success' ? 'alert-success' : 'alert-error'} mb-3`}>
            <span className="alert-icon">{tradeResult.action === 'buy' ? '💰' : tradeResult.type === 'success' ? '✅' : '❌'}</span>
            <span className="alert-message">{tradeResult.message}</span>
            <button 
              type="button"
              className="alert-close" 
              onClick={() => setTradeResult(null)}
              aria-label="Close notification"
            >×</button>
          </div>
        )}
        
        <form className="add-position-form" onSubmit={handleOpenTrade}>
          <div className="flex gap-3 mb-3">
            <input
              type="text"
              className="form-input flex-1"
              placeholder="Asset Symbol (e.g. BTCUSDT)"
              value={symbol}
              onChange={e => setSymbol(e.target.value)}
              disabled={loading}
            />
            <input
              type="number"
              className="form-input flex-1"
              placeholder="Amount to Buy"
              value={amount}
              onChange={e => setAmount(e.target.value)}
              step="any"
              disabled={loading}
            />
          </div>
          
          <div className="flex gap-3 items-center">
            <select 
              className="form-input"
              value={orderType}
              onChange={e => setOrderType(e.target.value)}
              disabled={loading}
              style={{ width: 'auto', minWidth: '120px' }}
            >
              <option value="market">Market Order</option>
              <option value="limit">Limit Order</option>
            </select>
            
            {orderType === 'limit' && (
              <input
                type="number"
                className="form-input flex-1"
                placeholder="Limit Price (USDT)"
                value={price}
                onChange={e => setPrice(e.target.value)}
                step="any"
                disabled={loading}
              />
            )}
            
            <button 
              type="submit" 
              className="btn-primary" 
              disabled={loading || !symbol || !amount}
            >
              {loading ? 'Executing...' : 'Open Position'}
            </button>
          </div>
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
                          type="button"
                          className="btn-danger"
                          onClick={() => openCloseTradeDialog(p)}
                          disabled={loading}
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

      <AlertDialog
        isOpen={closeTradeDialog.isOpen}
        onClose={closeTradeDialog.closeDialog}
        title={closeTradeDialog.dialog?.title}
        message={closeTradeDialog.dialog?.message}
        description={closeTradeDialog.dialog?.description}
        tone={closeTradeDialog.dialog?.tone}
        icon={closeTradeDialog.dialog?.icon}
        buttons={closeTradeDialog.dialog?.buttons || []}
      />
    </div>
  )
}

export default PositionsTable
