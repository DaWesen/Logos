import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import Layout from '@/components/Layout'
import ErrorBoundary from '@/components/ErrorBoundary'
import LoginPage from '@/pages/LoginPage'
import ChatPage from '@/pages/ChatPage'
import GroupPage from '@/pages/GroupPage'
import BotPage from '@/pages/BotPage'
import KnowledgePage from '@/pages/KnowledgePage'
import MCPPage from '@/pages/MCPPage'
import GraphPage from '@/pages/GraphPage'
import SummaryPage from '@/pages/SummaryPage'
import FriendsPage from '@/pages/FriendsPage'
import SettingsPage from '@/pages/SettingsPage'
import MonitoringPage from '@/pages/MonitoringPage'
import { BillingPage } from '@/pages/BillingPage'
import WalletPage from '@/pages/WalletPage'

function AuthGuard({ children }: { children: React.ReactNode }) {
  const token = localStorage.getItem('aim_token')
  if (!token) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <ErrorBoundary>
      <BrowserRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route
            path="/"
            element={
              <AuthGuard>
                <Layout />
              </AuthGuard>
            }
          >
            <Route index element={<Navigate to="/chat" replace />} />
            <Route path="chat" element={<ChatPage />} />
            <Route path="groups" element={<GroupPage />} />
            <Route path="bots" element={<BotPage />} />
            <Route path="knowledge" element={<KnowledgePage />} />
            <Route path="mcp" element={<MCPPage />} />
            <Route path="graph" element={<GraphPage />} />
            <Route path="summary" element={<SummaryPage />} />
            <Route path="friends" element={<FriendsPage />} />
            <Route path="monitoring" element={<MonitoringPage />} />
            <Route path="billing" element={<BillingPage />} />
            <Route path="wallet" element={<WalletPage />} />
            <Route path="settings" element={<SettingsPage />} />
          </Route>
          <Route path="*" element={<Navigate to="/chat" replace />} />
        </Routes>
      </BrowserRouter>
    </ErrorBoundary>
  )
}
