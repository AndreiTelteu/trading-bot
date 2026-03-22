import React, { useState, useEffect } from 'react'
import CustomSelect from './CustomSelect'
import { apiFetch } from '../services/api'

const API_BASE = '/api'

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
      const res = await apiFetch(`${API_BASE}/llm/config`)
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
      await apiFetch(`${API_BASE}/llm/config`, {
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
      const res = await apiFetch(`${API_BASE}/llm/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(config)
      })
      if (!res.ok) {
        let errorMessage = 'Test failed'
        try {
          const data = await res.json()
          if (data?.error) {
            errorMessage = data.error
          }
        } catch {}
        throw new Error(errorMessage)
      }
      setMessage({ type: 'success', text: 'Configuration is valid. Note: Actual LLM calls are disabled.' })
    } catch (err) {
      setMessage({ type: 'error', text: err.message || 'Test failed' })
    }
    setTesting(false)
  }

  if (loading) return <div>Loading...</div>

  const providers = [
    { value: 'openrouter', label: 'OpenRouter' },
    { value: 'openai', label: 'OpenAI' },
    { value: 'custom', label: 'Custom' },
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
          <input
            type="text"
            value={config.model}
            onChange={e => handleChange('model', e.target.value)}
            placeholder="google/gemini-2.0-flash-001"
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
