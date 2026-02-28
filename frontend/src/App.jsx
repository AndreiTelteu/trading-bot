import React, { useState, useEffect } from 'react'
import Dashboard from './components/Dashboard'
import PositionsTable from './components/PositionsTable'
import SettingsPanel from './components/SettingsPanel'
import AIProposal from './components/AIProposal'
import LLMConfig from './components/LLMConfig'
import ActivityLog from './components/ActivityLog'

const API_BASE = '/api'

function App() {
  const [activeTab, setActiveTab] = useState('dashboard')
  const [wallet, setWallet] = useState({ balance: 0, currency: 'USDT' })
  const [positions, setPositions] = useState([])
  const [socket, setSocket] = useState(null)
  const [connected, setConnected] = useState(false)
  const [showActivity, setShowActivity] = useState(true)
  const [isRunning, setIsRunning] = useState(false)

  useEffect(() => {
    fetchWallet()
    fetchPositions()
    
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws`
    const newSocket = new WebSocket(wsUrl)
    
    newSocket.onopen = () => {
      setConnected(true)
      newSocket.send(JSON.stringify({ type: 'join', room: 'main' }))
    }
    newSocket.onclose = () => setConnected(false)
    newSocket.onerror = () => setConnected(false)
    newSocket.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        if (data.type === 'balance_update') {
          setWallet(prev => ({ ...prev, balance: data.payload?.balance || data.new_balance }))
        } else if (data.type === 'position_update') {
          setPositions(prev => prev.map(p => 
            p.symbol === data.payload?.symbol ? { ...p, current_price: data.payload?.price, pnl: data.payload?.pnl } : p
          ))
        }
      } catch (e) {
        console.error('WS message parse error:', e)
      }
    }
    setSocket(newSocket)
    
    return () => newSocket.close()
  }, [])

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
          <div className={`connection-status ${connected ? 'connected' : 'disconnected'}`}>
            {connected ? 'Connected' : 'Disconnected'}
          </div>
        </div>
      </header>
      
      <nav className="nav">
        <button 
          className={activeTab === 'dashboard' ? 'active' : ''} 
          onClick={() => setActiveTab('dashboard')}
        >
          Dashboard
        </button>
        <button 
          className={activeTab === 'positions' ? 'active' : ''} 
          onClick={() => setActiveTab('positions')}
        >
          Positions
        </button>
        <button 
          className={activeTab === 'settings' ? 'active' : ''} 
          onClick={() => setActiveTab('settings')}
        >
          Settings
        </button>
        <button 
          className={activeTab === 'ai' ? 'active' : ''} 
          onClick={() => setActiveTab('ai')}
        >
          AI Proposals
        </button>
        <button 
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
