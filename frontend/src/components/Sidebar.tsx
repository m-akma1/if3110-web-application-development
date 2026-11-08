import React from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

interface SidebarProps {
  onClose?: () => void
}

export const Sidebar: React.FC<SidebarProps> = ({ onClose }) => {
  const { user, databases, logout } = useAuth()
  const navigate = useNavigate()

  const handleLogout = async () => {
    if (onClose) onClose()
    await logout()
    navigate('/login')
  }

  const handleNavClick = () => {
    if (onClose) onClose()
  }

  const linkClass = ({ isActive }: { isActive: boolean }) =>
    `flex items-center gap-3 px-3.5 py-2.5 rounded-xl text-xs font-semibold transition ${
      isActive
        ? 'bg-app-active text-zinc-100 border border-app-border-strong'
        : 'text-zinc-400 hover:bg-app-hover hover:text-zinc-200 border border-transparent'
    }`

  return (
    <aside className="w-60 bg-app-sidebar border border-app-border rounded-2xl flex flex-col justify-between shrink-0 p-2 h-full">
      <div>
        {/* Brand Name */}
        <div className="p-4 flex items-center justify-between">
          <span className="font-bold text-base text-zinc-100 tracking-wide">DB Manager</span>
          {onClose && (
            <button
              type="button"
              onClick={onClose}
              className="md:hidden p-1 text-zinc-400 hover:text-zinc-100 rounded-lg hover:bg-app-hover transition cursor-pointer"
              aria-label="Tutup menu"
            >
              <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          )}
        </div>

        {/* Navigation Links */}
        <nav className="mt-2 space-y-1">
          <NavLink to="/dashboard" onClick={handleNavClick} className={linkClass}>
            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4m0 5c0 2.21 3.582 4 8 4s8-1.79 8-4" />
            </svg>
            Database Viewer
          </NavLink>

          {user && databases && databases.length > 0 && (
            <NavLink to="/migration" onClick={handleNavClick} className={linkClass}>
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
              </svg>
              Migration Editor
            </NavLink>
          )}

          {user && user.is_platform_admin && (
            <NavLink to="/admin" onClick={handleNavClick} className={linkClass}>
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z" />
              </svg>
              Platform Admin
            </NavLink>
          )}
        </nav>
      </div>

      <div className="pt-2 border-t border-app-border">
        <button
          type="button"
          onClick={handleLogout}
          className="w-full flex items-center gap-3 px-3.5 py-2.5 rounded-xl text-xs font-semibold text-sem-danger hover:bg-sem-danger/10 transition cursor-pointer"
        >
          <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" />
          </svg>
          Keluar
        </button>
      </div>
    </aside>
  )
}
