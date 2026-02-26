import React, { useState, useEffect } from 'react'

const API_BASE = '/api'

function ActivityLog({ onRunAnalysis, isRunning }) {
  const [logs, setLogs] = useState([])
  const [filter, setFilter] = useState('all')

  useEffect(() => {
    fetchLogs()
    const interval = setInterval(fetchLogs, 5000)
    return () => clearInterval(interval)
  }, [])

  const fetchLogs = async () => {
    try {
      const res = await fetch(`${API_BASE}/activity-logs?limit=50`)
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`)
      }
      const data = await res.json()
      setLogs(data)
    } catch (err) {
      console.error('Failed to fetch activity logs:', err)
    }
  }

  const filteredLogs = filter === 'all' 
    ? logs 
    : logs.filter(log => log.log_type === filter)

  const getTypeColor = (type) => {
    switch (type) {
      case 'trade': return '#52b788'
      case 'analysis': return '#e9c46a'
      case 'system': return '#4cc9f0'
      case 'cron': return '#9d4edd'
      case 'error': return '#ff6b6b'
      default: return '#888'
    }
  }

  return (
    <div className="activity-log">
      <div className="activity-header">
        <h3>Activity Log</h3>
        <button 
          className={`btn-run ${isRunning ? 'running' : ''}`}
          onClick={onRunAnalysis}
          disabled={isRunning}
        >
          {isRunning ? 'Running...' : 'Run Analysis'}
        </button>
      </div>

      <div className="activity-filters">
        <button 
          className={filter === 'all' ? 'active' : ''} 
          onClick={() => setFilter('all')}
        >
          All
        </button>
        <button 
          className={filter === 'trade' ? 'active' : ''} 
          onClick={() => setFilter('trade')}
        >
          Trades
        </button>
        <button 
          className={filter === 'analysis' ? 'active' : ''} 
          onClick={() => setFilter('analysis')}
        >
          Analysis
        </button>
        <button 
          className={filter === 'system' ? 'active' : ''} 
          onClick={() => setFilter('system')}
        >
          System
        </button>
      </div>

      <div className="activity-list">
        {filteredLogs.length === 0 ? (
          <p className="no-data">No activity yet</p>
        ) : (
          filteredLogs.map((log) => (
            <div key={log.id} className={`activity-item ${log.log_type}`}>
              <div className="activity-meta">
                <span 
                  className="activity-type" 
                  style={{ color: getTypeColor(log.log_type) }}
                >
                  {log.log_type.toUpperCase()}
                </span>
                <span className="activity-time">
                  {new Date(log.timestamp).toLocaleTimeString()}
                </span>
              </div>
              <p className="activity-message">{log.message}</p>
              {log.details && (
                <p className="activity-details">{log.details}</p>
              )}
            </div>
          ))
        )}
      </div>
    </div>
  )
}

export default ActivityLog
