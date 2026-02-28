import React, { useState, useEffect, useCallback } from 'react'
import Dashboard from './components/Dashboard'
import PositionsTable from './components/PositionsTable'
import SettingsPanel from './components/SettingsPanel'
import AIProposal from './components/AIProposal'
import LLMConfig from './components/LLMConfig'
import ActivityLog from './components/ActivityLog'
import { useWebSocket, useWebSocketEvent } from './hooks/useWebSocket'
import { getWebSocketManager } from './services/websocketManager'

const API_BASE = '/api'

function App() {
  const [activeTab, setActiveTab] = useState('dashboard')
  const [wallet, setWallet] = useState({ balance: 0, currency: 'USDT' })
  const [positions, setPositions] = useState([])
  const [showActivity, setShowActivity] = useState(true)
  const [isRunning, setIsRunning] = useState(false)
  
  // Use WebSocket hook for connection state and send function
  const { isConnected, connectionState, send } = useWebSocket()

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

  // Initialize WebSocket connection on mount - only runs once
  useEffect(() => {
    // Fetch initial data via HTTP
    fetchWallet()
    fetchPositions()
    
    // WebSocket manager connects automatically when first component mounts
    const manager = getWebSocketManager()
    if (manager.getConnectionState() === 'disconnected') {
      manager.connect()
    }
    // No cleanup here - we want the WebSocket to persist for the session
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []) // Empty dependency array - only run once on mount

  // Listen for WebSocket events
  useWebSocketEvent('wallet_update', useCallback((data) => {
    setWallet(prev => ({
      ...prev,
      balance: data.balance ?? data.new_balance ?? prev.balance,
      currency: data.currency ?? prev.currency
    }))
  }, []))

  useWebSocketEvent('positions_update', useCallback((data) => {
    if (Array.isArray(data)) {
      setPositions(data)
    } else if (data.positions) {
      setPositions(data.positions)
    }
  }, []))

  useWebSocketEvent('position_update', useCallback((data) => {
    setPositions(prev => prev.map(p => 
      p.symbol === data.symbol 
        ? { ...p, ...data }
        : p
    ))
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

  const renderContent = () => {
    switch (activeTab) {
      case 'dashboard':
        return <Dashboard wallet={wallet} positions={positions} />
      case 'positions':
        return <PositionsTable positions={positions} onRefresh={fetchPositions} />
      case 'settings':
        return <SettingsPanel />
      case 'ai':
        return <AIProposal />
      case 'llm':
        return <LLMConfig />
      default:
        return <Dashboard wallet={wallet} positions={positions} />
    }
  }

  return (
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
        <button 
          type="button"
          className={activeTab === 'dashboard' ? 'active' : ''} 
          onClick={() => setActiveTab('dashboard')}
        >
          Dashboard
        </button>
        <button 
          type="button"
          className={activeTab === 'positions' ? 'active' : ''} 
          onClick={() => setActiveTab('positions')}
        >
          Positions
        </button>
        <button 
          type="button"
          className={activeTab === 'settings' ? 'active' : ''} 
          onClick={() => setActiveTab('settings')}
        >
          Settings
        </button>
        <button 
          type="button"
          className={activeTab === 'ai' ? 'active' : ''} 
          onClick={() => setActiveTab('ai')}
        >
          AI Proposals
        </button>
        <button 
          type="button"
          className={activeTab === 'llm' ? 'active' : ''} 
          onClick={() => setActiveTab('llm')}
        >
          LLM Config
        </button>
      </nav>
      
      <div className={`main ${showActivity ? 'with-sidebar' : ''}`}>
        <div className="content">
          {renderContent()}
        </div>
        {showActivity && (
          <div className="sidebar">
            <ActivityLog onRunAnalysis={handleRunAnalysis} isRunning={isRunning} />
          </div>
        )}
      </div>
    </div>
  )
}

export default App
