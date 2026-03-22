import React, { useState, useEffect } from 'react'
import { apiFetch } from '../services/api'

const API_BASE = '/api'

function AIProposal() {
  const [proposals, setProposals] = useState([])
  const [loading, setLoading] = useState(true)
  const [generating, setGenerating] = useState(false)
  const [filter, setFilter] = useState('all')
  const [typeFilter, setTypeFilter] = useState('all')

  useEffect(() => {
    fetchProposals()
  }, [])

  const fetchProposals = async () => {
    setLoading(true)
    try {
      const res = await apiFetch(`${API_BASE}/ai/proposals`)
      const data = await res.json()
      setProposals(data)
    } catch (err) {
      console.error('Failed to fetch proposals:', err)
    }
    setLoading(false)
  }

  const handleGenerate = async () => {
    setGenerating(true)
    try {
      const res = await apiFetch(`${API_BASE}/ai/generate-proposals`, { method: 'POST' })
      const data = await res.json()
      if (data.proposals) {
        fetchProposals()
      }
    } catch (err) {
      console.error('Failed to generate proposals:', err)
    }
    setGenerating(false)
  }

  const handleApprove = async (id) => {
    try {
      await apiFetch(`${API_BASE}/ai/proposals/${id}/approve`, { method: 'POST' })
      fetchProposals()
    } catch (err) {
      console.error('Failed to approve proposal:', err)
    }
  }

  const handleDeny = async (id) => {
    try {
      await apiFetch(`${API_BASE}/ai/proposals/${id}/deny`, { method: 'POST' })
      fetchProposals()
    } catch (err) {
      console.error('Failed to deny proposal:', err)
    }
  }

  if (loading) return <div>Loading...</div>

  const pending = proposals.filter(p => p.status === 'pending')
  const approved = proposals.filter(p => p.status === 'approved')
  const denied = proposals.filter(p => p.status === 'denied')
  const typeCounts = proposals.reduce((acc, proposal) => {
    const key = proposal.proposal_type || 'unknown'
    acc[key] = (acc[key] || 0) + 1
    return acc
  }, {})
  const statusFilteredProposals = filter === 'all'
    ? proposals
    : proposals.filter(p => p.status === filter)
  const filteredProposals = typeFilter === 'all'
    ? statusFilteredProposals
    : statusFilteredProposals.filter(p => p.proposal_type === typeFilter)
  const getProposalTypeLabel = (type) => {
    if (type === 'backtest_parameter_adjustment') return 'Backtest optimization'
    if (type === 'parameter_adjustment') return 'Market analysis'
    return type
  }

  return (
    <div className="ai-proposal">
      <div className="ai-header">
        <h2>AI Proposals</h2>
        <button 
          className="btn-generate"
          onClick={handleGenerate}
          disabled={generating}
        >
          {generating ? 'Generating...' : 'Generate Proposals'}
        </button>
      </div>

      <div className="filter-tabs">
        <button 
          className={filter === 'all' ? 'active' : ''}
          onClick={() => { setFilter('all') }}
        >
          All ({proposals.length})
        </button>
        <button 
          className={filter === 'pending' ? 'active' : ''}
          onClick={() => { setFilter('pending') }}
        >
          Pending ({pending.length})
        </button>
        <button 
          className={filter === 'approved' ? 'active' : ''}
          onClick={() => { setFilter('approved') }}
        >
          Approved ({approved.length})
        </button>
        <button 
          className={filter === 'denied' ? 'active' : ''}
          onClick={() => { setFilter('denied') }}
        >
          Denied ({denied.length})
        </button>
      </div>

      <div className="filter-tabs">
        <button
          className={typeFilter === 'all' ? 'active' : ''}
          onClick={() => { setTypeFilter('all') }}
        >
          All Types ({proposals.length})
        </button>
        <button
          className={typeFilter === 'parameter_adjustment' ? 'active' : ''}
          onClick={() => { setTypeFilter('parameter_adjustment') }}
        >
          Market Analysis ({typeCounts.parameter_adjustment || 0})
        </button>
        <button
          className={typeFilter === 'backtest_parameter_adjustment' ? 'active' : ''}
          onClick={() => { setTypeFilter('backtest_parameter_adjustment') }}
        >
          Backtest Optimization ({typeCounts.backtest_parameter_adjustment || 0})
        </button>
      </div>

      {filteredProposals.length === 0 ? (
        <p className="no-data">No proposals match the current filters.</p>
      ) : (
        <div className="proposals-list">
          {filteredProposals.map(p => (
            <div key={p.id} className={`proposal-card ${p.status}`}>
              <div className="proposal-header">
                <span className={`status-badge ${p.status}`}>{p.status}</span>
                <span className="proposal-type">{getProposalTypeLabel(p.proposal_type)}</span>
                <span className="proposal-date">
                  {new Date(p.created_at).toLocaleDateString()}
                </span>
              </div>
              
              <div className="proposal-body">
                <p className="parameter-change">
                  <strong>{p.parameter_key}</strong>: 
                  <span className="old-value"> {p.old_value}</span>
                  <span className="arrow"> → </span>
                  <span className="new-value">{p.new_value}</span>
                </p>
                <p className="reasoning">{p.reasoning}</p>
              </div>

              {p.status === 'pending' && (
                <div className="proposal-actions">
                  <button 
                    className="btn-approve"
                    onClick={() => handleApprove(p.id)}
                  >
                    Approve
                  </button>
                  <button 
                    className="btn-deny"
                    onClick={() => handleDeny(p.id)}
                  >
                    Deny
                  </button>
                </div>
              )}

              {p.resolved_at && (
                <p className="resolved-at">
                  Resolved: {new Date(p.resolved_at).toLocaleDateString()}
                </p>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

export default AIProposal
