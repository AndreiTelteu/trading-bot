import React, { useState, useEffect } from 'react'

const API_BASE = '/api'

const CustomSelect = ({ value, onChange, options }) => {
  const [isOpen, setIsOpen] = useState(false)
  const selectedOption = options.find(o => o.value === value) || options[0]

  return (
    <div className="custom-select-container" onMouseLeave={() => setIsOpen(false)}>
      <div className="custom-select-trigger" onClick={() => setIsOpen(!isOpen)}>
        <span>{selectedOption?.label}</span>
        <span className={`custom-select-arrow ${isOpen ? 'open' : ''}`}>▼</span>
      </div>
      {isOpen && (
        <div className="custom-select-dropdown">
          {options.map(opt => (
            <div 
              key={opt.value} 
              className={`custom-select-option ${opt.value === value ? 'selected' : ''}`}
              onClick={() => {
                onChange(opt.value)
                setIsOpen(false)
              }}
            >
              {opt.label}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function LLMConfig() {
  const [config, setConfig] = useState({
    provider: 'openrouter',
    base_url: '',
    api_key: '',
    model: ''
  })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [message, setMessage] = useState(null)

  useEffect(() => {
    fetchConfig()
  }, [])

  const fetchConfig = async () => {
    try {
      const res = await fetch(`${API_BASE}/llm/config`)
      const data = await res.json()
      setConfig(data)
    } catch (err) {
      console.error('Failed to fetch config:', err)
    }
    setLoading(false)
  }

  const handleChange = (field, value) => {
    setConfig(prev => ({ ...prev, [field]: value }))
  }

  const handleSave = async () => {
    setSaving(true)
    setMessage(null)
    try {
      await fetch(`${API_BASE}/llm/config`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config)
      })
      setMessage({ type: 'success', text: 'Configuration saved!' })
    } catch (err) {
      setMessage({ type: 'error', text: 'Failed to save configuration' })
    }
    setSaving(false)
  }

  const handleTest = async () => {
    setTesting(true)
    setMessage(null)
    try {
      await fetch(`${API_BASE}/llm/test`, { method: 'POST' })
      setMessage({ type: 'success', text: 'Configuration is valid. Note: Actual LLM calls are disabled.' })
    } catch (err) {
      setMessage({ type: 'error', text: 'Test failed' })
    }
    setTesting(false)
  }

  if (loading) return <div>Loading...</div>

  const providers = [
    { value: 'openrouter', label: 'OpenRouter' },
    { value: 'openai', label: 'OpenAI' },
    { value: 'custom', label: 'Custom' },
  ]

  const models = [
    { value: 'google/gemini-2.0-flash-001', label: 'Google Gemini 2.0 Flash' },
    { value: 'openai/gpt-4o', label: 'GPT-4o' },
    { value: 'openai/gpt-4o-mini', label: 'GPT-4o Mini' },
    { value: 'anthropic/claude-3.5-sonnet', label: 'Claude 3.5 Sonnet' },
  ]

  return (
    <div className="llm-config">
      <h2>LLM Configuration</h2>
      <p className="info-text">
        Configure the LLM provider for AI analysis. The actual LLM calls are disabled - 
        this UI is for configuration only.
      </p>

      <div className="config-form">
        <div className="form-group">
          <label>Provider</label>
          <CustomSelect
            value={config.provider}
            onChange={val => handleChange('provider', val)}
            options={providers}
          />
        </div>

        <div className="form-group">
          <label>Base URL</label>
          <input
            type="text"
            value={config.base_url}
            onChange={e => handleChange('base_url', e.target.value)}
            placeholder="https://openrouter.ai/api/v1"
          />
        </div>

        <div className="form-group">
          <label>API Key</label>
          <input
            type="password"
            value={config.api_key}
            onChange={e => handleChange('api_key', e.target.value)}
            placeholder="sk-..."
          />
        </div>

        <div className="form-group">
          <label>Model</label>
          <CustomSelect
            value={config.model}
            onChange={val => handleChange('model', val)}
            options={models}
          />
        </div>

        {message && (
          <div className={`message ${message.type}`}>
            {message.text}
          </div>
        )}

        <div className="button-group">
          <button 
            className="btn-save"
            onClick={handleSave}
            disabled={saving}
          >
            {saving ? 'Saving...' : 'Save Configuration'}
          </button>
          <button 
            className="btn-test"
            onClick={handleTest}
            disabled={testing}
          >
            {testing ? 'Testing...' : 'Test Connection'}
          </button>
        </div>
      </div>
    </div>
  )
}

export default LLMConfig
