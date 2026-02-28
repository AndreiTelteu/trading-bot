import React, { useState, useEffect } from 'react'

const API_BASE = '/api'

function SettingsPanel() {
  const [settings, setSettings] = useState({})
  const [weights, setWeights] = useState({})
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [activeSection, setActiveSection] = useState('trading')

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

  if (loading) return <div>Loading...</div>

  const tradingSettings = [
    { key: 'auto_trade_enabled', label: 'Auto Trade', type: 'boolean' },
    { key: 'entry_percent', label: 'Entry %', type: 'number', step: 0.1 },
    { key: 'stop_loss_percent', label: 'Stop Loss %', type: 'number', step: 0.1 },
    { key: 'take_profit_percent', label: 'Take Profit %', type: 'number', step: 0.1 },
    { key: 'rebuy_percent', label: 'Rebuy %', type: 'number', step: 0.1 },
    { key: 'max_positions', label: 'Max Positions', type: 'number' },
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
    : aiSettings

  return (
    <div className="settings-panel">
      <h2>Settings</h2>
      
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
                <select
                  value={settings[s.key] === true ? 'true' : 'false'}
                  onChange={e => handleSettingChange(s.key, e.target.value === 'true')}
                >
                  <option value="true">True</option>
                  <option value="false">False</option>
                </select>
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
        <div className="weights-form">
          <p className="weights-info">Adjust indicator weights (0-2)</p>
          {indicatorWeights.map(w => (
            <div key={w.key} className="weight-group">
              <label>{w.label}</label>
              <input
                type="range"
                min="0"
                max="2"
                step="0.1"
                value={weights[w.key] || 1.0}
                onChange={e => handleWeightChange(w.key, e.target.value)}
              />
              <span className="weight-value">{weights[w.key] || '1'}</span>
            </div>
          ))}
          <button className="btn-save" onClick={handleSaveWeights} disabled={saving}>
            {saving ? 'Saving...' : 'Save Weights'}
          </button>
        </div>
      )}
    </div>
  )
}

export default SettingsPanel
