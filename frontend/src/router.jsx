import React from 'react'
import {
  Navigate,
  Outlet,
  RouterProvider,
  createRootRoute,
  createRoute,
  createRouter,
} from '@tanstack/react-router'
import App, { useAppData } from './App.jsx'
import Dashboard from './components/Dashboard'
import PositionsTable from './components/PositionsTable'
import SettingsPanel from './components/SettingsPanel'
import AIProposal from './components/AIProposal'
import LLMConfig from './components/LLMConfig'

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
  component: App,
})

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: DashboardPage,
})

const positionsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: 'positions',
  component: PositionsPage,
})

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
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
  getParentRoute: () => rootRoute,
  path: 'ai',
  component: AIProposal,
})

const llmRoute = createRoute({
  getParentRoute: () => rootRoute,
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

const routeTree = rootRoute.addChildren([
  dashboardRoute,
  positionsRoute,
  settingsRoute,
  aiRoute,
  llmRoute,
])

const router = createRouter({
  routeTree,
})

export function AppRouter() {
  return <RouterProvider router={router} />
}
