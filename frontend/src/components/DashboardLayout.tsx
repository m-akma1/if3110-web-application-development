import React, { useState, useEffect } from 'react'
import { Outlet, Navigate, useLocation } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { Sidebar } from './Sidebar'
import { Navbar } from './Navbar'
import { ForbiddenPage } from '../pages/ForbiddenPage'

export const DashboardLayout: React.FC = () => {
  const { user, loading } = useAuth()
  const location = useLocation()
  const [mobileMenuOpen, setMobileMenuOpen] = useState(false)

  // Automatically close mobile menu when route changes
  useEffect(() => {
    setMobileMenuOpen(false)
  }, [location.pathname])

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

  const isForbiddenAdmin = location.pathname.startsWith('/admin') && !user.is_platform_admin

  return (
    <div className="flex h-screen overflow-hidden p-2 md:p-3 gap-3 bg-app-bg text-zinc-100 font-sans antialiased">
      {/* Desktop Sidebar */}
      <div className="hidden md:flex">
        <Sidebar />
      </div>

      {/* Mobile Drawer Overlay */}
      {mobileMenuOpen && (
        <div className="fixed inset-0 z-50 flex md:hidden">
          <div
            className="fixed inset-0 bg-black/70 backdrop-blur-xs transition-opacity"
            onClick={() => setMobileMenuOpen(false)}
          />
          <div className="relative z-10 w-64 h-full p-2">
            <Sidebar onClose={() => setMobileMenuOpen(false)} />
          </div>
        </div>
      )}

      {/* Main Container */}
      <div className="flex-1 flex flex-col min-w-0 overflow-hidden bg-app-sidebar rounded-2xl border border-app-border">
        <Navbar onToggleMenu={() => setMobileMenuOpen(!mobileMenuOpen)} />
        <main className="flex-1 overflow-y-auto p-4 md:p-6 bg-app-sidebar">
          {isForbiddenAdmin ? (
            <ForbiddenPage message="Hanya Platform Admin yang diizinkan mengakses halaman ini." />
          ) : (
            <Outlet />
          )}
        </main>
      </div>
    </div>
  )
}
