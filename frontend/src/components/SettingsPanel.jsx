import React, { useState, useEffect } from 'react'
import { createPortal } from 'react-dom'
import { Link } from '@tanstack/react-router'
import AlertDialog from './AlertDialog'
import CustomSelect from './CustomSelect'
import useAlertDialog from '../hooks/useAlertDialog'
import { useWebSocketEvent } from '../hooks/useWebSocket'
import { apiFetch } from '../services/api'

const API_BASE = '/api'

const normalizeBacktestJob = (job) => {
  if (!job) return job
  if (job.summary || typeof job.summary_json !== 'string' || job.summary_json.trim() === '') {
    return job
  }

  try {
    return {
      ...job,
      summary: JSON.parse(job.summary_json),
    }
  } catch {
    return job
  }
}

const formatBacktestDate = (value) => {
  if (!value) return 'Unknown time'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return 'Unknown time'
  return date.toLocaleString()
}

const getBacktestSummary = (job) => job?.summary || null

const getBacktestMetrics = (job, strategyKey) => {
  const summary = getBacktestSummary(job)
  const strategy = summary?.[strategyKey]
  return strategy?.Metrics || strategy?.metrics || {}
}

const getBacktestSymbols = (job) => {
  const summary = getBacktestSummary(job)
  const equityBySymbol = summary?.baseline?.EquityBySymbol || summary?.baseline?.equityBySymbol || {}
  return Object.keys(equityBySymbol)
}

const formatMetricValue = (value, digits = 2, suffix = '', multiplier = 1, prefix = '') => {
  const num = Number(value)
  if (!Number.isFinite(num)) return '—'
  return `${prefix}${(num * multiplier).toFixed(digits)}${suffix}`
}

const OPTIMIZATION_MODE_LABELS = {
  strict: 'Strict pass',
  hypothesis_fallback: 'Best-effort fallback',
  none: 'No accepted proposals',
}

const formatOptimizationMode = (mode) => OPTIMIZATION_MODE_LABELS[mode] || mode || 'Unknown'

const formatOptimizationReason = (reason) => {
  if (!reason) return 'Unknown reason'
  return reason
    .split('_')
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

function BacktestOptimizationDialogContent({ result }) {
  const proposals = Array.isArray(result?.proposals) ? result.proposals : []
  const attempts = Array.isArray(result?.attempts) ? result.attempts : []

  return (
    <div style={{ display: 'grid', gap: '1rem' }}>
      <div className="job-meta-info">
        <div className="meta-item">
          <span className="meta-label">Backtest</span>
          <span className="meta-value">#{result?.job_id || '—'}</span>
        </div>
        <div className="meta-item">
          <span className="meta-label">Created</span>
          <span className="meta-value">{result?.count || 0}</span>
        </div>
        <div className="meta-item">
          <span className="meta-label">Mode</span>
          <span className="meta-value">{formatOptimizationMode(result?.attempt_mode)}</span>
        </div>
      </div>

      {proposals.length > 0 && (
        <div>
          <div className="text-muted text-sm" style={{ marginBottom: '0.5rem' }}>Accepted proposals</div>
          <div style={{ display: 'grid', gap: '0.65rem' }}>
            {proposals.map((proposal, index) => (
              <div key={proposal.id || `${proposal.parameter_key}-${index}`} className="glass-panel" style={{ padding: '0.85rem 1rem' }}>
                <div className="metric-row">
                  <span className="metric-name">{proposal.parameter_key || 'Parameter'}</span>
                  <span className="metric-value">{proposal.old_value ?? '—'} → {proposal.new_value ?? '—'}</span>
                </div>
                {proposal.reasoning && (
                  <p className="text-muted text-sm" style={{ margin: '0.6rem 0 0' }}>
                    {proposal.reasoning}
                  </p>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {attempts.length > 0 && (
        <div>
          <div className="text-muted text-sm" style={{ marginBottom: '0.5rem' }}>Attempt diagnostics</div>
          <div style={{ display: 'grid', gap: '0.65rem' }}>
            {attempts.map((attempt, index) => {
              const rejectedEntries = Object.entries(attempt?.diagnostics?.rejected_counts || {})

              return (
                <div key={`${attempt.mode || 'attempt'}-${index}`} className="glass-panel" style={{ padding: '0.85rem 1rem' }}>
                  <div className="metric-row">
                    <span className="metric-name">{formatOptimizationMode(attempt.mode)}</span>
                    <span className="metric-value">{attempt.accepted_count || 0} accepted</span>
                  </div>
                  {attempt.finish_reason && (
                    <p className="text-muted text-sm" style={{ margin: '0.6rem 0 0' }}>
                      Finish reason: <span className="metric-value">{attempt.finish_reason}</span>
                    </p>
                  )}
                  {attempt.error ? (
                    <p className="negative text-sm" style={{ margin: '0.6rem 0 0' }}>{attempt.error}</p>
                  ) : rejectedEntries.length > 0 ? (
                    <>
                      <ul style={{ margin: '0.6rem 0 0', paddingLeft: '1.2rem' }}>
                        {rejectedEntries.map(([reason, count]) => (
                          <li key={reason} className="text-muted text-sm">
                            {formatOptimizationReason(reason)}: {count}
                          </li>
                        ))}
                      </ul>
                      {attempt.raw_response && (
                        <div style={{ marginTop: '0.75rem' }}>
                          <div className="text-muted text-sm" style={{ marginBottom: '0.4rem' }}>
                            Raw AI response
                          </div>
                          <textarea
                            readOnly
                            value={attempt.raw_response}
                            className="modal-textarea"
                            style={{ minHeight: '160px' }}
                          />
                        </div>
                      )}
                    </>
                  ) : (
                    <p className="text-muted text-sm" style={{ margin: '0.6rem 0 0' }}>No validation rejections.</p>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}

const SETTINGS_SECTIONS = [
  { key: 'trading', label: 'Trading', to: '/settings/trading' },
  { key: 'indicators', label: 'Indicators', to: '/settings/indicators' },
  { key: 'probabilistic', label: 'Probabilistic', to: '/settings/probabilistic' },
  { key: 'ai', label: 'AI Settings', to: '/settings/ai' },
  { key: 'atr', label: 'ATR', to: '/settings/atr' },
  { key: 'backtest', label: 'Backtest', to: '/settings/backtest' },
  { key: 'weights', label: 'Weights', to: '/settings/weights' },
]

function SettingsPanel({ activeSection }) {
  const [settings, setSettings] = useState({})
  const [weights, setWeights] = useState({})
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [modalOpen, setModalOpen] = useState(false)
  const [modalMode, setModalMode] = useState('export')
  const [modalText, setModalText] = useState('')
  const [modalError, setModalError] = useState('')
  const [backtestJob, setBacktestJob] = useState(null)
  const [backtestJobs, setBacktestJobs] = useState([])
  const [selectedBacktestId, setSelectedBacktestId] = useState('')
  const [startingBacktest, setStartingBacktest] = useState(false)
  const [optimizingBacktest, setOptimizingBacktest] = useState(false)
  const optimizeBacktestDialog = useAlertDialog()

  useEffect(() => {
    fetchSettings()
    fetchWeights()
    fetchLatestBacktest()
    fetchBacktestJobs()
  }, [])

  const fetchSettings = async () => {
    try {
      const res = await apiFetch(`${API_BASE}/settings`)
      const data = await res.json()
      // API returns an array of {key, value, ...} objects — convert to a key->value map
      const normalized = {}
      for (const item of data) {
        const v = item.value
        if (typeof v === 'string') {
          const lowerValue = v.toLowerCase()
          if (lowerValue === 'true') normalized[item.key] = true
          else if (lowerValue === 'false') normalized[item.key] = false
          else normalized[item.key] = v
        } else {
          normalized[item.key] = v
        }
      }
      setSettings(normalized)
    } catch (err) {
      console.error('Failed to fetch settings:', err)
    }
    setLoading(false)
  }

  const fetchWeights = async () => {
    try {
      const res = await apiFetch(`${API_BASE}/indicator-weights`)
      const data = await res.json()
      // API returns an array of {indicator, weight} objects — convert to indicator->weight map
      const weightsMap = {}
      for (const item of data) {
        weightsMap[item.indicator] = item.weight
      }
      setWeights(weightsMap)
    } catch (err) {
      console.error('Failed to fetch weights:', err)
    }
  }

  const fetchLatestBacktest = async () => {
    try {
      const res = await apiFetch(`${API_BASE}/backtest/latest`)
      if (!res.ok) {
        return
      }
      const data = await res.json()
      const normalized = normalizeBacktestJob(data)
      setBacktestJob(normalized)
      upsertBacktestJob(normalized)
    } catch (err) {
      console.error('Failed to fetch backtest status:', err)
    }
  }

  const fetchBacktestJobs = async () => {
    try {
      const res = await apiFetch(`${API_BASE}/backtest/jobs`)
      if (!res.ok) {
        return
      }
      const data = await res.json()
      const normalizedJobs = Array.isArray(data) ? data.map(normalizeBacktestJob) : []
      setBacktestJobs(normalizedJobs)
      setSelectedBacktestId(prev => prev || (normalizedJobs[0] ? String(normalizedJobs[0].id) : ''))
    } catch (err) {
      console.error('Failed to fetch backtest jobs:', err)
    }
  }

  const upsertBacktestJob = (job) => {
    if (!job || !job.id) return
    const normalized = normalizeBacktestJob(job)
    setBacktestJobs(prev => {
      const existing = prev.find(item => item.id === normalized.id)
      const next = existing
        ? prev.map(item => item.id === normalized.id ? { ...item, ...normalized } : item)
        : [normalized, ...prev]
      next.sort((a, b) => new Date(b.created_at || 0).getTime() - new Date(a.created_at || 0).getTime())
      return next
    })
    setSelectedBacktestId(prev => prev || String(normalized.id))
  }

  const handleSettingChange = (key, value) => {
    setSettings(prev => ({ ...prev, [key]: value }))
  }

  const handleWeightChange = (indicator, value) => {
    setWeights(prev => ({ ...prev, [indicator]: parseFloat(value) }))
  }

  const handleSaveSettings = async () => {
    setSaving(true)
    try {
      // Backend expects an array of {key, value} objects
      const payload = Object.entries(settings).map(([key, value]) => ({
        key,
        value: String(value)
      }))
      await apiFetch(`${API_BASE}/settings`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      })
      alert('Settings saved!')
    } catch (err) {
      console.error('Failed to save settings:', err)
      alert('Failed to save settings')
    }
    setSaving(false)
  }

  const handleSaveWeights = async () => {
    setSaving(true)
    try {
      // Backend expects an array of {indicator, weight} objects
      const payload = Object.entries(weights).map(([indicator, weight]) => ({
        indicator,
        weight: parseFloat(weight)
      }))
      await apiFetch(`${API_BASE}/indicator-weights`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      })
      alert('Weights saved!')
    } catch (err) {
      console.error('Failed to save weights:', err)
      alert('Failed to save weights')
    }
    setSaving(false)
  }

  const handleStartBacktest = async () => {
    setStartingBacktest(true)
    try {
      const res = await apiFetch(`${API_BASE}/backtest/start`, { method: 'POST' })
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`)
      }
      const data = await res.json()
      const normalized = normalizeBacktestJob(data)
      setBacktestJob(normalized)
      upsertBacktestJob(normalized)
      setSelectedBacktestId(String(normalized.id))
    } catch (err) {
      console.error('Failed to start backtest:', err)
      alert('Failed to start backtest')
    }
    setStartingBacktest(false)
  }

  useWebSocketEvent('backtest_status', (data) => {
    const normalized = normalizeBacktestJob(data)
    setBacktestJob(normalized)
    upsertBacktestJob(normalized)
  })

  useWebSocketEvent('backtest_progress', (data) => {
    setBacktestJob(prev => {
      const next = {
        ...(prev || {}),
        id: data.job_id,
        status: data.status,
        progress: data.progress,
        message: data.message,
      }
      upsertBacktestJob(next)
      if (!prev || prev.id === data.job_id) {
        return next
      }
      return prev
    })
  })

  useWebSocketEvent('backtest_complete', (data) => {
    setBacktestJob(prev => {
      const next = normalizeBacktestJob({
        ...(prev || {}),
        id: data.job_id,
        status: data.status,
        progress: 1,
        summary: data.summary,
      })
      upsertBacktestJob(next)
      if (!prev || prev.id === data.job_id) {
        return next
      }
      return prev
    })
  })

  const handleOptimizeBacktest = async () => {
    if (!selectedBacktestId) return

    setOptimizingBacktest(true)
    try {
      const res = await apiFetch(`${API_BASE}/ai/optimize-backtest`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ job_id: Number(selectedBacktestId) })
      })
      const data = await res.json()
      if (!res.ok) {
        const error = new Error(data.error || `HTTP ${res.status}`)
        error.details = data
        error.status = res.status
        throw error
      }

      const hasProposals = Array.isArray(data.proposals) && data.proposals.length > 0
      optimizeBacktestDialog.openDialog({
        tone: hasProposals ? 'success' : 'warning',
        title: hasProposals ? 'Backtest optimization complete' : 'No optimization proposals created',
        message: data.message || (hasProposals
          ? `Created ${data.count || 0} backtest optimization proposal(s).`
          : 'The AI response did not produce any accepted proposals.'),
        description: hasProposals && data.used_fallback
          ? 'The fallback hypothesis pass was used to turn the selected backtest into proposal candidates.'
          : `Selected backtest: #${data.job_id || selectedBacktestId}`,
        buttons: [
          {
            label: 'Close',
            variant: 'primary',
            autoFocus: true,
            closeOnClick: true,
          },
        ],
        children: <BacktestOptimizationDialogContent result={data} />,
      })
    } catch (err) {
      console.error('Failed to optimize backtest:', err)
      optimizeBacktestDialog.openDialog({
        tone: 'danger',
        title: 'Backtest optimization failed',
        message: err.message || 'Failed to optimize backtest',
        description: `Selected backtest: #${selectedBacktestId}`,
        buttons: [
          {
            label: 'Close',
            variant: 'primary',
            autoFocus: true,
            closeOnClick: true,
          },
        ],
        children: err?.details ? (
          <div className="job-meta-info">
            <div className="meta-item">
              <span className="meta-label">HTTP Status</span>
              <span className="meta-value">{err.status || '—'}</span>
            </div>
            <div className="meta-item">
              <span className="meta-label">Error</span>
              <span className="meta-value">{err.details.error || err.message}</span>
            </div>
          </div>
        ) : null,
      })
    }
    setOptimizingBacktest(false)
  }

  const openExportModal = () => {
    const payload = {
      settings,
      weights,
    }
    setModalMode('export')
    setModalText(JSON.stringify(payload, null, 2))
    setModalError('')
    setModalOpen(true)
  }

  const openImportModal = () => {
    setModalMode('import')
    setModalText('')
    setModalError('')
    setModalOpen(true)
  }

  const closeModal = () => {
    setModalOpen(false)
    setModalText('')
    setModalError('')
  }

  const handleImportSettings = async () => {
    setSaving(true)
    setModalError('')
    let parsed
    try {
      parsed = JSON.parse(modalText)
    } catch (err) {
      setModalError('Invalid JSON')
      setSaving(false)
      return
    }

    const nextSettings = parsed && typeof parsed.settings === 'object' && !Array.isArray(parsed.settings)
      ? parsed.settings
      : {}
    const nextWeights = parsed && typeof parsed.weights === 'object' && !Array.isArray(parsed.weights)
      ? parsed.weights
      : {}

    setSettings(nextSettings)
    setWeights(nextWeights)

    try {
      const settingsPayload = Object.entries(nextSettings).map(([key, value]) => ({
        key,
        value: String(value)
      }))
      await apiFetch(`${API_BASE}/settings`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(settingsPayload)
      })

      const weightsPayload = Object.entries(nextWeights).map(([indicator, weight]) => ({
        indicator,
        weight: parseFloat(weight)
      }))
      await apiFetch(`${API_BASE}/indicator-weights`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(weightsPayload)
      })

      alert('Settings imported!')
      closeModal()
    } catch (err) {
      console.error('Failed to import settings:', err)
      alert('Failed to import settings')
    }
    setSaving(false)
  }

  if (loading) return <div>Loading...</div>

  const tradingSettings = [
    { key: 'auto_trade_enabled', label: 'Auto Trade', type: 'boolean' },
    { key: 'entry_percent', label: 'Entry %', type: 'number', step: 0.1 },
    { key: 'vol_sizing_enabled', label: 'Vol Sizing Enabled', type: 'boolean' },
    { key: 'risk_per_trade', label: 'Risk Per Trade %', type: 'number', step: 0.1 },
    { key: 'stop_mult', label: 'ATR Stop Mult', type: 'number', step: 0.1 },
    { key: 'tp_mult', label: 'ATR TP Mult', type: 'number', step: 0.1 },
    { key: 'max_position_value', label: 'Max Position Value', type: 'number', step: 1 },
    { key: 'time_stop_bars', label: 'Time Stop Bars', type: 'number', step: 1 },
    { key: 'stop_loss_percent', label: 'Stop Loss %', type: 'number', step: 0.1 },
    { key: 'take_profit_percent', label: 'Take Profit %', type: 'number', step: 0.1 },
    { key: 'rebuy_percent', label: 'Rebuy %', type: 'number', step: 0.1 },
    { key: 'max_positions', label: 'Max Positions', type: 'number' },
    { key: 'trending_coins_to_analyze', label: 'Trending Coins Analysis', type: 'number' },
    { key: 'regime_gate_enabled', label: 'Regime Gate Enabled', type: 'boolean' },
    { key: 'regime_timeframe', label: 'Regime Timeframe', type: 'text' },
    { key: 'regime_ema_fast', label: 'Regime EMA Fast', type: 'number' },
    { key: 'regime_ema_slow', label: 'Regime EMA Slow', type: 'number' },
    { key: 'vol_atr_period', label: 'Vol ATR Period', type: 'number' },
    { key: 'vol_ratio_min', label: 'Vol Ratio Min', type: 'number', step: 0.0001 },
    { key: 'vol_ratio_max', label: 'Vol Ratio Max', type: 'number', step: 0.0001 },
    { key: 'buy_only_strong', label: 'Buy Only Strong', type: 'boolean' },
    { key: 'sell_on_signal', label: 'Sell On Signal', type: 'boolean' },
    { key: 'min_confidence_to_buy', label: 'Min Confidence Buy', type: 'number', step: 0.1 },
    { key: 'min_confidence_to_sell', label: 'Min Confidence Sell', type: 'number', step: 0.1 },
    { key: 'allow_sell_at_loss', label: 'Allow Sell At Loss', type: 'boolean' },
    { key: 'trailing_stop_enabled', label: 'Trailing Stop', type: 'boolean' },
    { key: 'trailing_stop_percent', label: 'Trailing Stop %', type: 'number', step: 0.1 },
    { key: 'pyramiding_enabled', label: 'Pyramiding', type: 'boolean' },
    { key: 'max_pyramid_layers', label: 'Max Pyramid Layers', type: 'number' },
    { key: 'position_scale_percent', label: 'Position Scale %', type: 'number', step: 0.1 },
  ]

  const indicatorSettings = [
    { key: 'macd_fast_period', label: 'MACD Fast', type: 'number' },
    { key: 'macd_slow_period', label: 'MACD Slow', type: 'number' },
    { key: 'macd_signal_period', label: 'MACD Signal', type: 'number' },
    { key: 'rsi_period', label: 'RSI Period', type: 'number' },
    { key: 'rsi_oversold', label: 'RSI Oversold', type: 'number' },
    { key: 'rsi_overbought', label: 'RSI Overbought', type: 'number' },
    { key: 'bb_period', label: 'Bollinger Period', type: 'number' },
    { key: 'bb_std', label: 'Bollinger Std', type: 'number', step: 0.1 },
    { key: 'volume_ma_period', label: 'Volume MA Period', type: 'number' },
    { key: 'momentum_period', label: 'Momentum Period', type: 'number' },
  ]

  const probabilisticSettings = [
    { key: 'prob_model_enabled', label: 'Prob Model Enabled', type: 'boolean' },
    { key: 'prob_model_beta0', label: 'Prob Beta 0', type: 'number', step: 0.0001 },
    { key: 'prob_model_beta1', label: 'Prob Beta 1', type: 'number', step: 0.0001 },
    { key: 'prob_model_beta2', label: 'Prob Beta 2', type: 'number', step: 0.0001 },
    { key: 'prob_model_beta3', label: 'Prob Beta 3', type: 'number', step: 0.0001 },
    { key: 'prob_model_beta4', label: 'Prob Beta 4', type: 'number', step: 0.0001 },
    { key: 'prob_model_beta5', label: 'Prob Beta 5', type: 'number', step: 0.0001 },
    { key: 'prob_model_beta6', label: 'Prob Beta 6', type: 'number', step: 0.0001 },
    { key: 'prob_p_min', label: 'Prob P Min', type: 'number', step: 0.0001 },
    { key: 'prob_ev_min', label: 'Prob EV Min', type: 'number', step: 0.0001 },
    { key: 'prob_avg_gain', label: 'Prob Avg Gain', type: 'number', step: 0.0001 },
    { key: 'prob_avg_loss', label: 'Prob Avg Loss', type: 'number', step: 0.0001 },
  ]

  const atrSettings = [
    { key: 'atr_trailing_enabled', label: 'ATR Trailing Enabled', type: 'boolean' },
    { key: 'atr_trailing_mult', label: 'ATR Trailing Mult', type: 'number', step: 0.1 },
    { key: 'atr_trailing_period', label: 'ATR Trailing Period', type: 'number' },
    { key: 'atr_annualization_enabled', label: 'ATR Annualization', type: 'boolean' },
    { key: 'atr_annualization_days', label: 'ATR Annualization Days', type: 'number' },
  ]

  const backtestSettings = [
    { key: 'backtest_symbols', label: 'Backtest Symbols', type: 'text' },
    { key: 'backtest_start', label: 'Backtest Start (YYYY-MM-DD or RFC3339)', type: 'text' },
    { key: 'backtest_end', label: 'Backtest End (YYYY-MM-DD or RFC3339)', type: 'text' },
    { key: 'backtest_fee_bps', label: 'Backtest Fee (bps)', type: 'number', step: 1 },
    { key: 'backtest_slippage_bps', label: 'Backtest Slippage (bps)', type: 'number', step: 1 },
  ]

  const aiSettings = [
    { key: 'ai_analysis_interval', label: 'Analysis Interval (hours)', type: 'number' },
    { key: 'ai_lookback_days', label: 'Lookback Days', type: 'number' },
    { key: 'ai_min_proposals', label: 'Min Proposals', type: 'number' },
    { key: 'ai_change_budget_pct', label: 'Max Numeric Change (%)', type: 'number', step: 0.1 },
    { key: 'ai_auto_apply_days', label: 'Auto Apply Days', type: 'number' },
  ]

  const indicatorWeights = [
    { key: 'macd', label: 'MACD' },
    { key: 'rsi', label: 'RSI' },
    { key: 'bollinger', label: 'Bollinger Bands' },
    { key: 'volume', label: 'Volume' },
    { key: 'momentum', label: 'Momentum' },
  ]

  const backtestProgress = backtestJob?.progress != null ? Math.min(1, Math.max(0, backtestJob.progress)) : null
  const backtestProgressPercent = backtestProgress != null ? Math.round(backtestProgress * 100) : null
  const selectedBacktestJob = backtestJobs.find(job => String(job.id) === selectedBacktestId) || null
  const selectedBacktestHasSummary = Boolean(getBacktestSummary(selectedBacktestJob))
  const backtestOptions = backtestJobs.map(job => {
    const symbols = getBacktestSymbols(job)
    const suffix = symbols.length > 0 ? ` • ${symbols.join(', ')}` : ''
    return {
      value: String(job.id),
      label: `#${job.id} • ${job.status || 'unknown'} • ${formatBacktestDate(job.created_at)}${suffix}`,
    }
  })
  const backtestMetricRows = [
    { key: 'TradeCount', label: 'Trade Count', digits: 0 },
    { key: 'WinRate', label: 'Win Rate', digits: 2, suffix: '%', multiplier: 100 },
    { key: 'ProfitFactor', label: 'Profit Factor', digits: 2 },
    { key: 'AvgWin', label: 'Avg Win', digits: 2, prefix: '$' },
    { key: 'AvgLoss', label: 'Avg Loss', digits: 2, prefix: '$' },
  ]

  const currentSettings = activeSection === 'trading' ? tradingSettings 
    : activeSection === 'indicators' ? indicatorSettings 
    : activeSection === 'probabilistic' ? probabilisticSettings
    : activeSection === 'atr' ? atrSettings
    : activeSection === 'backtest' ? backtestSettings
    : aiSettings

  const modalContent = (
    <div className="modal-overlay">
      <div className="modal-panel">
        <div className="modal-header">
          <h3>{modalMode === 'export' ? 'Export Settings' : 'Import Settings'}</h3>
          <button className="modal-close" onClick={closeModal}>×</button>
        </div>
        <div className="modal-body">
          <textarea
            className="modal-textarea"
            value={modalText}
            onChange={e => setModalText(e.target.value)}
            readOnly={modalMode === 'export'}
          />
          {modalError && <div className="modal-error">{modalError}</div>}
        </div>
        <div className="modal-actions">
          {modalMode === 'import' && (
            <button className="btn-primary" onClick={handleImportSettings} disabled={saving}>
              {saving ? 'Importing...' : 'Import Settings'}
            </button>
          )}
          <button className="btn-danger" onClick={closeModal}>Close</button>
        </div>
      </div>
    </div>
  )

  return (
    <div className="settings-panel">
      <div className="settings-header">
        <h2>Settings</h2>
        <div className="settings-actions">
          <button className="btn-primary" onClick={openExportModal}>Export Settings</button>
          <button className="btn-danger" onClick={openImportModal}>Import Settings</button>
        </div>
      </div>
      
      <div className="settings-tabs">
        {SETTINGS_SECTIONS.map(section => (
          <Link
            key={section.key}
            to={section.to}
            className={activeSection === section.key ? 'active' : ''}
          >
            {section.label}
          </Link>
        ))}
      </div>

      {activeSection !== 'weights' ? (
        <div className="settings-form">
          {currentSettings.map(s => (
            <div key={s.key} className="form-group">
              <label htmlFor={`setting-${s.key}`}>{s.label}</label>
              {s.type === 'boolean' ? (
                <CustomSelect
                  value={settings[s.key] === true ? 'true' : 'false'}
                  onChange={val => handleSettingChange(s.key, val === 'true')}
                  options={[
                    { value: 'true', label: 'True' },
                    { value: 'false', label: 'False' }
                  ]}
                  id={`setting-${s.key}`}
                />
              ) : (
                <input
                  id={`setting-${s.key}`}
                  type={s.type}
                  step={s.step || 1}
                  value={settings[s.key] || ''}
                  onChange={e => handleSettingChange(s.key, e.target.value)}
                />
              )}
            </div>
          ))}
          <button className="btn-save" onClick={handleSaveSettings} disabled={saving}>
            {saving ? 'Saving...' : 'Save Settings'}
          </button>
          {activeSection === 'backtest' && (
            <div className="backtest-controls-wrapper glass-panel fade-in">
              <div className="backtest-header-bar">
                <div className="backtest-action-buttons">
                  <button className="btn-primary" onClick={handleStartBacktest} disabled={startingBacktest || backtestJob?.status === 'running'}>
                    <span className="btn-icon">▶</span>
                    {startingBacktest || backtestJob?.status === 'running' ? 'Running...' : 'Run Backtest'}
                  </button>
                  <button className="btn-save btn-optimize" onClick={handleOptimizeBacktest} disabled={optimizingBacktest || !selectedBacktestId || !selectedBacktestHasSummary}>
                    <span className="btn-icon">✨</span>
                    {optimizingBacktest ? 'Optimizing...' : 'AI Optimize'}
                  </button>
                </div>
                {backtestJob && (
                  <div className="backtest-status-pill">
                    <span className={`status-dot ${backtestJob.status === 'running' ? 'running' : 'completed'}`}></span>
                    Status: {backtestJob.status || 'unknown'} {backtestProgressPercent != null ? `(${backtestProgressPercent}%)` : ''}
                    {backtestJob.message ? ` - ${backtestJob.message}` : ''}
                  </div>
                )}
              </div>

              {backtestProgressPercent != null && (
                <div className="backtest-progress-bar-container">
                  <div className="progress-header">
                    <span className="text-muted text-sm font-bold uppercase">Backtest Progress</span>
                    <span className="text-accent text-sm font-mono">{backtestProgressPercent}%</span>
                  </div>
                  <div className="progress-track">
                    <div className="progress-fill" style={{ width: `${backtestProgressPercent}%` }} />
                  </div>
                </div>
              )}

              <div className="backtest-results-section">
                  <div className="backtest-selector-card">
                    <div className="form-group selector-group" style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: '0.75rem', border: 'none' }}>
                      <label htmlFor="backtest-select" style={{ color: 'var(--text-muted)' }}>Select Stored Backtest</label>
                      <CustomSelect
                        id="backtest-select"
                        value={selectedBacktestId || backtestOptions[0]?.value}
                        onChange={setSelectedBacktestId}
                        options={backtestOptions}
                        className="backtest-select"
                      />
                    </div>
                  </div>

                {selectedBacktestJob && (
                  <div className="selected-job-details">
                    <div className="job-meta-info">
                      <div className="meta-item">
                        <span className="meta-label">Job ID</span>
                        <span className="meta-value">#{selectedBacktestJob.id}</span>
                      </div>
                      <div className="meta-item">
                        <span className="meta-label">Date</span>
                        <span className="meta-value">{formatBacktestDate(selectedBacktestJob.created_at)}</span>
                      </div>
                      <div className="meta-item">
                        <span className="meta-label">Status</span>
                        <span className={`meta-value status-${selectedBacktestJob.status}`}>{selectedBacktestJob.status || 'unknown'}</span>
                      </div>
                      {selectedBacktestJob.error && (
                        <div className="meta-item error-item">
                          <span className="meta-label">Error</span>
                          <span className="meta-value error-text">{selectedBacktestJob.error}</span>
                        </div>
                      )}
                    </div>
                    
                    {selectedBacktestJob.summary?.validation && (
                      <div className={`validation-banner ${selectedBacktestJob.summary.validation.passed ? 'passed' : 'failed'}`}>
                        <span className="validation-icon">{selectedBacktestJob.summary.validation.passed ? '✓' : '✗'}</span>
                        Validation: {selectedBacktestJob.summary.validation.passed ? 'Passed' : 'Failed'} ({selectedBacktestJob.summary.validation.windows} windows)
                      </div>
                    )}
                    
                    {selectedBacktestHasSummary ? (
                      <div className="metrics-comparison-grid">
                        {['baseline', 'vol_sizing'].map(strategyKey => {
                          const metrics = getBacktestMetrics(selectedBacktestJob, strategyKey)
                          return (
                            <div key={strategyKey} className="metric-card">
                              <h4 className="metric-card-title">
                                {strategyKey === 'vol_sizing' ? 'Vol Sizing Strategy' : 'Baseline Strategy'}
                              </h4>
                              <div className="metric-list">
                                  {backtestMetricRows.map(metric => {
                                    const rawVal = metrics?.[metric.key]
                                    let formattedVal = formatMetricValue(rawVal, metric.digits, metric.suffix || '', metric.multiplier || 1, metric.prefix || '')
                                    let colorClass = rawVal < 0 ? 'negative' : rawVal > 0 ? 'positive' : ''
                                    
                                    if (metric.key === 'ProfitFactor' && Number.isFinite(Number(rawVal))) {
                                      const num = Number(rawVal)
                                      const pct = (num - 1) * 100
                                      const sign = pct > 0 ? '+' : ''
                                      colorClass = '' // Keep the main number neutral
                                      const pctColor = pct < 0 ? 'negative' : pct > 0 ? 'positive' : ''
                                      formattedVal = (
                                        <>
                                          {formatMetricValue(rawVal, metric.digits, metric.suffix || '', metric.multiplier || 1, metric.prefix || '')} <span className={pctColor}>({sign}{pct.toFixed(0)}%)</span>
                                        </>
                                      )
                                    } else if (metric.key === 'WinRate') {
                                      colorClass = rawVal < 0.5 ? 'negative' : rawVal > 0.5 ? 'positive' : ''
                                    } else if (metric.key === 'TradeCount') {
                                      colorClass = ''
                                    }

                                    return (
                                      <div key={metric.key} className="metric-row">
                                        <span className="metric-name">{metric.label}</span>
                                        <span className={`metric-value ${colorClass}`}>
                                          {formattedVal}
                                        </span>
                                      </div>
                                    )
                                  })}
                              </div>
                            </div>
                          )
                        })}
                      </div>
                    ) : (
                      <div className="empty-state">This backtest has no stored summary, so AI optimization is unavailable.</div>
                    )}
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      ) : (
        <div className="weights-form fade-in">
          <div className="weights-header">
            <h3 className="title-gradient text-lg">AI Analyzer Weights</h3>
            <p className="text-muted text-sm pb-4">Fine-tune the importance of each indicator (0-2 scale). Values update dynamically.</p>
          </div>
          <div className="weights-grid">
            {indicatorWeights.map(w => {
              const val = weights[w.key] ?? 1.0;
              const percentage = (val / 2) * 100;
              return (
                <div key={w.key} className="weight-item glass-panel">
                  <div className="weight-label">
                     <label htmlFor={`weight-${w.key}`}>{w.label}</label>
                     <p className="weight-multiplier text-muted font-mono">Multiplier: {val}x</p>
                  </div>
                  
                  <div className="weight-slider-wrapper">
                    <div className="slider-track-bg">
                       <div className="slider-fill-active" style={{ width: `${percentage}%` }}></div>
                    </div>
                    <input
                      id={`weight-${w.key}`}
                      type="range"
                      min="0"
                      max="2"
                      step="0.1"
                      value={val}
                      onChange={e => handleWeightChange(w.key, e.target.value)}
                      className="custom-range-slider"
                   />
                  </div>
                  
                  <div className="weight-value-display bg-black font-mono">
                    <span className="text-accent">{val.toFixed(1)}</span>
                  </div>
                </div>
              )
            })}
          </div>
          <div className="weights-actions">
            <button className="btn-primary" onClick={handleSaveWeights} disabled={saving}>
              {saving ? 'Saving...' : 'Apply Weights'}
            </button>
          </div>
        </div>
      )}
      {modalOpen && createPortal(modalContent, document.body)}
      <AlertDialog
        isOpen={optimizeBacktestDialog.isOpen}
        onClose={optimizeBacktestDialog.closeDialog}
        title={optimizeBacktestDialog.dialog?.title}
        message={optimizeBacktestDialog.dialog?.message}
        description={optimizeBacktestDialog.dialog?.description}
        tone={optimizeBacktestDialog.dialog?.tone}
        icon={optimizeBacktestDialog.dialog?.icon}
        buttons={optimizeBacktestDialog.dialog?.buttons || []}
      >
        {optimizeBacktestDialog.dialog?.children}
      </AlertDialog>
    </div>
  )
}

export default SettingsPanel



