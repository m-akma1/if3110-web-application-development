import React from 'react'
import { Routes, Route, Navigate } from 'react-router-dom'
import { LoginPage } from './pages/LoginPage'
import { SignupPage } from './pages/SignupPage'
import { DashboardLayout } from './components/DashboardLayout'
import { DashboardPage } from './pages/DashboardPage'
import { MigrationPage } from './pages/MigrationPage'
import { MigrationDetailPage } from './pages/MigrationDetailPage'
import { AdminPage } from './pages/AdminPage'
import { ForbiddenPage } from './pages/ForbiddenPage'
import { ErrorBoundary } from './components/ErrorBoundary'

export const App: React.FC = () => {
  return (
    <ErrorBoundary>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/signup" element={<SignupPage />} />

        <Route element={<DashboardLayout />}>
          <Route path="/" element={<Navigate to="/dashboard" replace />} />
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/migration" element={<MigrationPage />} />
          <Route path="/migration/:id" element={<MigrationDetailPage />} />
          <Route path="/admin" element={<AdminPage />} />
          <Route path="/forbidden" element={<ForbiddenPage />} />
        </Route>

        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </ErrorBoundary>
  )
}
