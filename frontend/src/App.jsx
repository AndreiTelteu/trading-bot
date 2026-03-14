import React, { useState, useEffect, useCallback, useMemo, useContext } from 'react'
import { Link, Outlet } from '@tanstack/react-router'
import ActivityLog from './components/ActivityLog'
import { useWebSocket, useWebSocketEvent } from './hooks/useWebSocket'
import { getWebSocketManager } from './services/websocketManager'

const API_BASE = '/api'

const mergePositions = (prevPositions, incomingPositions) => {
  const closedPositions = prevPositions.filter(position => position.status === 'closed')
  const mergedOpenPositions = incomingPositions.map((incomingPosition) => {
    const existingPosition = prevPositions.find(position => position.id === incomingPosition.id || position.symbol === incomingPosition.symbol)
    return existingPosition ? { ...existingPosition, ...incomingPosition } : incomingPosition
  })

  return [...mergedOpenPositions, ...closedPositions]
}

const upsertPosition = (prevPositions, nextPosition) => {
  const index = prevPositions.findIndex(position => position.id === nextPosition.id || position.symbol === nextPosition.symbol)
  if (index === -1) {
    return [nextPosition, ...prevPositions]
  }

  const updatedPositions = [...prevPositions]
  updatedPositions[index] = { ...updatedPositions[index], ...nextPosition }
  return updatedPositions
}

const AppDataContext = React.createContext(null)

export const useAppData = () => {
  const context = useContext(AppDataContext)

  if (!context) {
    throw new Error('useAppData must be used within App')
  }

  return context
}

function App() {
  const [wallet, setWallet] = useState({ balance: 0, currency: 'USDT' })
  const [positions, setPositions] = useState([])
  const [showActivity, setShowActivity] = useState(true)
  const [isRunning, setIsRunning] = useState(false)

  const { isConnected } = useWebSocket()

  const fetchWallet = useCallback(async () => {
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
  }, [])

  const fetchPositions = useCallback(async () => {
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
  }, [])

  useEffect(() => {
    fetchWallet()
    fetchPositions()

    const manager = getWebSocketManager()
    if (manager.getConnectionState() === 'disconnected') {
      manager.connect()
    }
  }, [fetchPositions, fetchWallet])

  useWebSocketEvent('wallet_update', useCallback((data) => {
    setWallet(prev => ({
      ...prev,
      balance: data.balance ?? data.new_balance ?? prev.balance,
      currency: data.currency ?? prev.currency
    }))
  }, []))

  useWebSocketEvent('positions_update', useCallback((data) => {
    if (Array.isArray(data)) {
      setPositions(prev => mergePositions(prev, data))
    } else if (data.positions) {
      setPositions(prev => mergePositions(prev, data.positions))
    }
  }, []))

  useWebSocketEvent('position_update', useCallback((data) => {
    setPositions(prev => upsertPosition(prev, data))
  }, []))

  useWebSocketEvent('position_closed', useCallback((data) => {
    setPositions(prev => prev.map(p =>
      p.id === data.position_id || p.symbol === data.symbol
        ? { ...p, status: 'closed', close_reason: data.reason, pnl: data.pnl }
        : p
    ))
  }, []))

  const handleTradeExecuted = useCallback((data) => {
    // Refresh data after trade execution
    fetchWallet()
    fetchPositions()
  }, [fetchWallet, fetchPositions])

  useWebSocketEvent('trade_executed', handleTradeExecuted)

  const handleRunAnalysis = async () => {
    setIsRunning(true)
    try {
      const res = await fetch(`${API_BASE}/trending/analyze`, { method: 'POST' })
      const data = await res.json()
      console.log('Analysis result:', data)
    } catch (err) {
      console.error('Failed to run analysis:', err)
    } finally {
      setIsRunning(false)
    }
  }

  const appData = useMemo(() => ({
    wallet,
    positions,
    fetchPositions,
  }), [wallet, positions, fetchPositions])

  return (
    <AppDataContext.Provider value={appData}>
      <div className="app">
        <header className="header">
          <h1>Trading Bot</h1>
          <div className="header-actions">
            <button
              type="button"
              className={`activity-toggle ${showActivity ? 'active' : ''}`}
              onClick={() => setShowActivity(!showActivity)}
            >
              Activity Log
            </button>
            <div className={`connection-status ${isConnected ? 'connected' : 'disconnected'}`}>
              {isConnected ? 'Connected' : 'Disconnected'}
            </div>
          </div>
        </header>

        <nav className="nav">
          <Link to="/" className="nav-link" activeProps={{ className: 'active' }}>Dashboard</Link>
          <Link to="/positions" className="nav-link" activeProps={{ className: 'active' }}>Positions</Link>
          <Link to="/settings" className="nav-link" activeProps={{ className: 'active' }}>Settings</Link>
          <Link to="/ai" className="nav-link" activeProps={{ className: 'active' }}>AI Proposals</Link>
          <Link to="/llm" className="nav-link" activeProps={{ className: 'active' }}>LLM Config</Link>
        </nav>

        <div className={`main ${showActivity ? 'with-sidebar' : ''}`}>
          <div className="content">
            <Outlet />
          </div>
          {showActivity && (
            <div className="sidebar">
              <ActivityLog onRunAnalysis={handleRunAnalysis} isRunning={isRunning} />
            </div>
          )}
        </div>
      </div>
    </AppDataContext.Provider>
  )
}

export default App
