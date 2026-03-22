import React, { useState, useEffect, useCallback } from 'react'
import { useWebSocketEvent } from '../hooks/useWebSocket'
import { apiFetch } from '../services/api'

const API_BASE = '/api'

function ActivityLog({ onRunAnalysis, isRunning }) {
  const [logs, setLogs] = useState([])
  const [filter, setFilter] = useState('all')

  const fetchLogs = useCallback(async () => {
    try {
      const res = await apiFetch(`${API_BASE}/activity-logs?limit=50`)
      if (res.ok) setLogs(await res.json())
    } catch (err) {}
  }, [])

  // Fetch initial logs on mount
  useEffect(() => {
    fetchLogs()
  }, [fetchLogs])

  // Listen for new activity logs via WebSocket
  useWebSocketEvent('activity_log_new', useCallback((newLog) => {
    setLogs(prev => [newLog, ...prev].slice(0, 50)) // Keep last 50 logs
  }, []))

  // Listen for bulk logs (initial sync or reconnect)
  useWebSocketEvent('activity_log_bulk', useCallback((bulkLogs) => {
    if (Array.isArray(bulkLogs)) {
      setLogs(bulkLogs)
    }
  }, []))

  const filteredLogs = filter === 'all' 
    ? logs 
    : logs.filter(log => log.log_type === filter)

  return (
    <div className="activity-log glass-panel p-0">
      <div className="activity-header flex justify-between items-center">
        <h3>Activity Log</h3>
        <button 
          type="button"
          className={`btn-run-analysis ${isRunning ? 'running' : ''}`}
          onClick={onRunAnalysis}
          disabled={isRunning}
        >
          {isRunning ? <><span className="spinner"></span> Running</> : '▶ Run Analysis'}
        </button>
      </div>

      <div className="activity-filters scroll-x">
        {['all', 'trade', 'analysis', 'system'].map(f => (
          <button 
            key={f}
            type="button"
            className={`filter-badge ${filter === f ? 'active' : ''}`} 
            onClick={() => setFilter(f)}
          >
            {f.charAt(0).toUpperCase() + f.slice(1)}
          </button>
        ))}
      </div>

      <div className="activity-list fancy-scroll">
        {filteredLogs.length === 0 ? (
          <div className="empty-state">
            <span className="empty-icon">📝</span>
            <p>No activity yet</p>
          </div>
        ) : (
          filteredLogs.map((log) => (
            <div key={log.id} className={`activity-item type-${log.log_type}`}>
              <div className="activity-timeline">
                <div className={`timeline-dot dot-${log.log_type}`}></div>
                <div className="timeline-line"></div>
              </div>
              <div className="activity-content">
                <div className="activity-meta">
                  <div className="activity-main-line">
                    <span className={`badge-type badge-${log.log_type}`}>
                      {log.log_type.toUpperCase()}
                    </span>
                    <div className="message-scroll-container">
                      <span className="activity-message">{log.message}</span>
                    </div>
                  </div>
                  <span className="activity-time">
                    {new Date(log.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
                  </span>
                </div>
                {log.details && (
                  <div className="activity-details-box">
                    <p>{log.details}</p>
                  </div>
                )}
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  )
}

export default ActivityLog
