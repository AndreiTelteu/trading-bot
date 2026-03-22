import React, { useState } from 'react'
import { Navigate, useNavigate } from '@tanstack/react-router'
import { useAuth } from './AuthProvider'

function LoginPage() {
  const navigate = useNavigate()
  const { isAuthenticated, isLoading, login } = useAuth()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  if (isLoading) {
    return (
      <div className="login-screen">
        <div className="login-shell glass-panel">
          <p className="login-kicker">Checking session</p>
          <h1 className="login-title">Trading Bot</h1>
          <p className="login-subtitle">Preparing your operator console...</p>
        </div>
      </div>
    )
  }

  if (isAuthenticated) {
    return <Navigate to="/" replace />
  }

  const handleSubmit = async (event) => {
    event.preventDefault()
    setSubmitting(true)
    setError('')

    try {
      await login({ username, password })
      navigate({ to: '/' })
    } catch (err) {
      setError(err.message || 'Login failed')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="login-screen">
      <div className="login-shell glass-panel">
        <p className="login-kicker">Secure operator access</p>
        <h1 className="login-title">Trading Bot</h1>
        <p className="login-subtitle">
          Sign in to access the dashboard, trading controls, AI proposals, and live market activity.
        </p>

        <form className="login-form" onSubmit={handleSubmit}>
          <label className="login-field">
            <span>Username</span>
            <input
              type="text"
              className="form-input"
              value={username}
              onChange={event => setUsername(event.target.value)}
              autoComplete="username"
              disabled={submitting}
              required
            />
          </label>

          <label className="login-field">
            <span>Password</span>
            <input
              type="password"
              className="form-input"
              value={password}
              onChange={event => setPassword(event.target.value)}
              autoComplete="current-password"
              disabled={submitting}
              required
            />
          </label>

          {error && <div className="login-error">{error}</div>}

          <button type="submit" className="btn-primary login-submit" disabled={submitting}>
            {submitting ? 'Signing in...' : 'Enter Dashboard'}
          </button>
        </form>
      </div>
    </div>
  )
}

export default LoginPage
