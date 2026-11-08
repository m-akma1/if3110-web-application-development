import React from 'react'
import { Outlet, Navigate, useLocation } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { Sidebar } from './Sidebar'
import { Navbar } from './Navbar'
import { ForbiddenPage } from '../pages/ForbiddenPage'

export const DashboardLayout: React.FC = () => {
  const { user, loading } = useAuth()
  const location = useLocation()

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-app-bg text-zinc-400 text-xs font-mono">
        Memuat sesi...
      </div>
    )
  }

  if (!user) {
    return <Navigate to="/login" replace state={{ from: location }} />
  }

  if (location.pathname.startsWith('/admin') && !user.is_platform_admin) {
    return (
      <div className="flex h-screen overflow-hidden p-3 gap-3 bg-app-bg text-zinc-100 font-sans antialiased">
        <Sidebar />
        <div className="flex-1 flex flex-col min-w-0 overflow-hidden bg-app-sidebar rounded-2xl border border-app-border">
          <Navbar />
          <main className="flex-1 overflow-y-auto p-6 bg-app-sidebar">
            <ForbiddenPage message="Hanya Platform Admin yang diizinkan mengakses halaman ini." />
          </main>
        </div>
      </div>
    )
  }

  return (
    <div className="flex h-screen overflow-hidden p-3 gap-3 bg-app-bg text-zinc-100 font-sans antialiased">
      <Sidebar />
      <div className="flex-1 flex flex-col min-w-0 overflow-hidden bg-app-sidebar rounded-2xl border border-app-border">
        <Navbar />
        <main className="flex-1 overflow-y-auto p-6 bg-app-sidebar">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
