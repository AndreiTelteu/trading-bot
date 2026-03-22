import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { getWebSocketManager } from '../services/websocketManager'
import { AUTH_UNAUTHORIZED_EVENT, apiFetch } from '../services/api'

const AuthContext = createContext(null)

const guestState = {
  isLoading: false,
  isAuthenticated: false,
  username: '',
}

export function AuthProvider({ children }) {
  const [authState, setAuthState] = useState({
    ...guestState,
    isLoading: true,
  })

  const checkSession = useCallback(async () => {
    try {
      const response = await apiFetch('/api/auth/session', {}, { suppressUnauthorizedRedirect: true })
      if (!response.ok) {
        setAuthState(guestState)
        return false
      }

      const data = await response.json()
      setAuthState({
        isLoading: false,
        isAuthenticated: true,
        username: data.username || '',
      })
      return true
    } catch (error) {
      setAuthState(guestState)
      return false
    }
  }, [])

  useEffect(() => {
    checkSession()
  }, [checkSession])

  useEffect(() => {
    const handleUnauthorized = () => {
      getWebSocketManager().disconnect()
      setAuthState(guestState)
    }

    window.addEventListener(AUTH_UNAUTHORIZED_EVENT, handleUnauthorized)
    return () => window.removeEventListener(AUTH_UNAUTHORIZED_EVENT, handleUnauthorized)
  }, [])

  const login = useCallback(async ({ username, password }) => {
    const response = await apiFetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password }),
    }, { suppressUnauthorizedRedirect: true })

    const data = await response.json().catch(() => ({}))
    if (!response.ok) {
      throw new Error(data.error || `HTTP ${response.status}`)
    }

    setAuthState({
      isLoading: false,
      isAuthenticated: true,
      username: data.username || username,
    })

    return data
  }, [])

  const logout = useCallback(async () => {
    try {
      await apiFetch('/api/auth/logout', { method: 'POST' }, { suppressUnauthorizedRedirect: true })
    } finally {
      getWebSocketManager().disconnect()
      setAuthState(guestState)
    }
  }, [])

  const value = useMemo(() => ({
    ...authState,
    checkSession,
    login,
    logout,
  }), [authState, checkSession, login, logout])

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const context = useContext(AuthContext)

  if (!context) {
    throw new Error('useAuth must be used within AuthProvider')
  }

  return context
}
