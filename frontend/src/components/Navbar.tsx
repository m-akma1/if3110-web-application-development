import React from 'react'
import { useAuth } from '../context/AuthContext'

function accessLabel(level: string): string {
  switch (level) {
    case 'VIEW_EDIT':
      return 'View + Edit'
    case 'VIEW_ONLY':
      return 'View Only'
    default:
      return 'Tanpa akses'
  }
}

export const Navbar: React.FC = () => {
  const { user, databases, activeDBID, setActiveDBID } = useAuth()

  return (
    <header className="h-16 w-full border-b border-app-border px-6 flex items-center justify-between shrink-0 bg-app-sidebar">
      <div className="flex items-center gap-3">
        <span className="text-xs font-semibold text-zinc-400 uppercase tracking-wider">Database:</span>
        {user && databases && databases.length > 0 ? (
          <select
            value={activeDBID}
            aria-label="Pilih database"
            onChange={(e) => setActiveDBID(e.target.value)}
            className="bg-app-input border border-app-border text-zinc-200 text-xs font-mono rounded-xl px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-brand-primary/50 cursor-pointer"
          >
            {databases.map((db) => (
              <option key={db.database_id} value={db.database_id}>
                {db.database_name} - {accessLabel(db.effective_access)}
              </option>
            ))}
          </select>
        ) : (
          <span className="text-xs text-sem-warning font-mono bg-sem-warning/10 px-2.5 py-1 rounded-md border border-sem-warning/20">
            Belum ada akses database
          </span>
        )}
      </div>

      <div className="flex items-center gap-2.5">
        {user ? (
          <>
            {user.is_platform_admin && (
              <span className="text-[10px] font-bold tracking-wider uppercase bg-brand-primary text-app-bg px-2.5 py-1 rounded-lg">
                ADMIN
              </span>
            )}
            <span className="text-xs font-semibold text-zinc-200">{user.username}</span>
          </>
        ) : (
          <span className="text-xs font-medium text-zinc-400">Guest</span>
        )}
      </div>
    </header>
  )
}
