import React from 'react'
import {
  Navigate,
  Outlet,
  RouterProvider,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import { AuthProvider, useAuth } from './components/AuthProvider'
import App, { useAppData } from './App.jsx'
import Dashboard from './components/Dashboard'
import PositionsTable from './components/PositionsTable'
import SettingsPanel from './components/SettingsPanel'
import AIProposal from './components/AIProposal'
import LLMConfig from './components/LLMConfig'
import LoginPage from './components/LoginPage'

function RootLayout() {
  return (
    <AuthProvider>
      <Outlet />
    </AuthProvider>
  )
}

function ProtectedLayout() {
  const { isAuthenticated, isLoading } = useAuth()

  if (isLoading) {
    return (
      <div className="auth-loading-screen">
        <div className="glass-panel auth-loading-panel">
          <p className="login-kicker">Checking session</p>
          <h1 className="login-title">Trading Bot</h1>
        </div>
      </div>
    )
  }

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />
  }

  return <App />
}

function DashboardPage() {
  const { wallet, positions } = useAppData()

  return <Dashboard wallet={wallet} positions={positions} />
}

function PositionsPage() {
  const { positions, fetchPositions } = useAppData()

  return <PositionsTable positions={positions} onRefresh={fetchPositions} />
}

function SettingsLayout() {
  return <Outlet />
}

function SettingsSectionPage({ section }) {
  return <SettingsPanel activeSection={section} />
}

const rootRoute = createRootRoute({
  component: RootLayout,
})

const protectedRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'protected',
  component: ProtectedLayout,
})

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'login',
  component: LoginPage,
})

const dashboardRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: '/',
  component: DashboardPage,
})

const positionsRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: 'positions',
  component: PositionsPage,
})

const settingsRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: 'settings',
  component: SettingsLayout,
})

const settingsIndexRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: '/',
  component: () => <Navigate to="/settings/trading" replace />,
})

const settingsTradingRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'trading',
  component: () => <SettingsSectionPage section="trading" />,
})

const settingsIndicatorsRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'indicators',
  component: () => <SettingsSectionPage section="indicators" />,
})

const settingsProbabilisticRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'probabilistic',
  component: () => <SettingsSectionPage section="probabilistic" />,
})

const settingsAiRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'ai',
  component: () => <SettingsSectionPage section="ai" />,
})

const settingsAtrRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'atr',
  component: () => <SettingsSectionPage section="atr" />,
})

const settingsBacktestRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'backtest',
  component: () => <SettingsSectionPage section="backtest" />,
})

const settingsWeightsRoute = createRoute({
  getParentRoute: () => settingsRoute,
  path: 'weights',
  component: () => <SettingsSectionPage section="weights" />,
})

const aiRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: 'ai',
  component: AIProposal,
})

const llmRoute = createRoute({
  getParentRoute: () => protectedRoute,
  path: 'llm',
  component: LLMConfig,
})

settingsRoute.addChildren([
  settingsIndexRoute,
  settingsTradingRoute,
  settingsIndicatorsRoute,
  settingsProbabilisticRoute,
  settingsAiRoute,
  settingsAtrRoute,
  settingsBacktestRoute,
  settingsWeightsRoute,
])

protectedRoute.addChildren([
  dashboardRoute,
  positionsRoute,
  settingsRoute,
  aiRoute,
  llmRoute,
])

const routeTree = rootRoute.addChildren([
  loginRoute,
  protectedRoute,
])

const router = createRouter({
  routeTree,
})

export function AppRouter() {
  return <RouterProvider router={router} />
}
