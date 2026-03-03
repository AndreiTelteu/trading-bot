import React, { useState, useEffect } from 'react'
import { createPortal } from 'react-dom'
import CustomSelect from './CustomSelect'

const API_BASE = '/api'

function SettingsPanel() {
  const [settings, setSettings] = useState({})
  const [weights, setWeights] = useState({})
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [activeSection, setActiveSection] = useState('trading')
  const [modalOpen, setModalOpen] = useState(false)
  const [modalMode, setModalMode] = useState('export')
  const [modalText, setModalText] = useState('')
  const [modalError, setModalError] = useState('')

  useEffect(() => {
    fetchSettings()
    fetchWeights()
  }, [])

  const fetchSettings = async () => {
    try {
      const res = await fetch(`${API_BASE}/settings`)
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
      const res = await fetch(`${API_BASE}/indicator-weights`)
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
      await fetch(`${API_BASE}/settings`, {
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
      await fetch(`${API_BASE}/indicator-weights`, {
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
      await fetch(`${API_BASE}/settings`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(settingsPayload)
      })

      const weightsPayload = Object.entries(nextWeights).map(([indicator, weight]) => ({
        indicator,
        weight: parseFloat(weight)
      }))
      await fetch(`${API_BASE}/indicator-weights`, {
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

  const aiSettings = [
    { key: 'ai_analysis_interval', label: 'Analysis Interval (hours)', type: 'number' },
    { key: 'ai_lookback_days', label: 'Lookback Days', type: 'number' },
    { key: 'ai_min_proposals', label: 'Min Proposals', type: 'number' },
    { key: 'ai_auto_apply_days', label: 'Auto Apply Days', type: 'number' },
  ]

  const indicatorWeights = [
    { key: 'macd', label: 'MACD' },
    { key: 'rsi', label: 'RSI' },
    { key: 'bollinger', label: 'Bollinger Bands' },
    { key: 'volume', label: 'Volume' },
    { key: 'momentum', label: 'Momentum' },
  ]

  const currentSettings = activeSection === 'trading' ? tradingSettings 
    : activeSection === 'indicators' ? indicatorSettings 
    : activeSection === 'probabilistic' ? probabilisticSettings
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
        <button 
          className={activeSection === 'trading' ? 'active' : ''}
          onClick={() => setActiveSection('trading')}
        >
          Trading
        </button>
        <button 
          className={activeSection === 'indicators' ? 'active' : ''}
          onClick={() => setActiveSection('indicators')}
        >
          Indicators
        </button>
        <button 
          className={activeSection === 'probabilistic' ? 'active' : ''}
          onClick={() => setActiveSection('probabilistic')}
        >
          Probabilistic
        </button>
        <button 
          className={activeSection === 'ai' ? 'active' : ''}
          onClick={() => setActiveSection('ai')}
        >
          AI Settings
        </button>
        <button 
          className={activeSection === 'weights' ? 'active' : ''}
          onClick={() => setActiveSection('weights')}
        >
          Weights
        </button>
      </div>

      {activeSection !== 'weights' ? (
        <div className="settings-form">
          {currentSettings.map(s => (
            <div key={s.key} className="form-group">
              <label>{s.label}</label>
              {s.type === 'boolean' ? (
                <CustomSelect
                  value={settings[s.key] === true ? 'true' : 'false'}
                  onChange={val => handleSettingChange(s.key, val === 'true')}
                  options={[
                    { value: 'true', label: 'True' },
                    { value: 'false', label: 'False' }
                  ]}
                />
              ) : (
                <input
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
                     <label>{w.label}</label>
                     <p className="weight-multiplier text-muted font-mono">Multiplier: {val}x</p>
                  </div>
                  
                  <div className="weight-slider-wrapper">
                    <div className="slider-track-bg">
                       <div className="slider-fill-active" style={{ width: `${percentage}%` }}></div>
                    </div>
                    <input
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
    </div>
  )
}

export default SettingsPanel
