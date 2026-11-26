import React, { useState, useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { api } from '../api/client'
import { AdminOverview } from '../types'
import { AdminUsersTab } from '../components/admin/AdminUsersTab'
import { AdminGroupsTab } from '../components/admin/AdminGroupsTab'
import { AdminTargetsTab } from '../components/admin/AdminTargetsTab'
import { AdminCodesTab } from '../components/admin/AdminCodesTab'

export const AdminPage: React.FC = () => {
  const { user } = useAuth()
  const [searchParams, setSearchParams] = useSearchParams()
  const activeTab = searchParams.get('tab') || 'users'

  const [loading, setLoading] = useState(true)
  const [overview, setOverview] = useState<AdminOverview | null>(null)
  const [flash, setFlash] = useState<{ success: boolean; message: string } | null>(null)

  const loadOverview = async () => {
    try {
      const data = await api.getAdminOverview()
      setOverview(data)
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message || 'Gagal memuat data admin' })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadOverview()
  }, [])

  const setTab = (t: string) => {
    setSearchParams({ tab: t })
  }

  if (loading) {
    return <div className="text-xs text-zinc-400">Memuat konsol admin...</div>
  }

  if (!overview) {
    return <div className="text-xs text-sem-danger">Gagal memuat konsol admin</div>
  }

  const users = overview.users || (overview as any).Users || []
  const groups = overview.groups || (overview as any).Groups || []
  const targets = overview.targets || (overview as any).Targets || []
  const codes = overview.codes || (overview as any).Codes || []

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-white tracking-tight">Platform Administration</h1>
        <p className="text-zinc-400 mt-1 text-xs">
          Kelola pengguna, group, koneksi database, dan kode undangan. Akses database diberikan per group; setiap user selalu menjadi anggota group Public.
        </p>
      </div>

      {/* Tabs */}
      <div className="flex flex-wrap gap-2 border-b border-app-border pb-3">
        <button
          type="button"
          onClick={() => setTab('users')}
          className={`px-4 py-2 rounded-xl text-xs transition cursor-pointer ${
            activeTab === 'users' ? 'bg-brand-primary text-app-bg font-bold' : 'text-zinc-400 hover:bg-app-hover hover:text-zinc-200'
          }`}
        >
          Pengguna
        </button>
        <button
          type="button"
          onClick={() => setTab('groups')}
          className={`px-4 py-2 rounded-xl text-xs transition cursor-pointer ${
            activeTab === 'groups' ? 'bg-brand-primary text-app-bg font-bold' : 'text-zinc-400 hover:bg-app-hover hover:text-zinc-200'
          }`}
        >
          Group & Akses
        </button>
        <button
          type="button"
          onClick={() => setTab('databases')}
          className={`px-4 py-2 rounded-xl text-xs transition cursor-pointer ${
            activeTab === 'databases' ? 'bg-brand-primary text-app-bg font-bold' : 'text-zinc-400 hover:bg-app-hover hover:text-zinc-200'
          }`}
        >
          Koneksi Database
        </button>
        <button
          type="button"
          onClick={() => setTab('codes')}
          className={`px-4 py-2 rounded-xl text-xs transition cursor-pointer ${
            activeTab === 'codes' ? 'bg-brand-primary text-app-bg font-bold' : 'text-zinc-400 hover:bg-app-hover hover:text-zinc-200'
          }`}
        >
          Kode Undangan
        </button>
      </div>

      {/* Flash Banner */}
      {flash && (
        <div
          role={flash.success ? 'status' : 'alert'}
          className={`p-3 rounded-xl border text-xs ${
            flash.success ? 'bg-sem-success/10 border-sem-success/30 text-sem-success' : 'bg-sem-danger/10 border-sem-danger/30 text-sem-danger'
          }`}
        >
          {flash.message}
        </div>
      )}

      {activeTab === 'users' && (
        <AdminUsersTab
          currentUser={user}
          users={users}
          groups={groups}
          onFlash={setFlash}
          onReload={loadOverview}
        />
      )}

      {activeTab === 'groups' && (
        <AdminGroupsTab
          groups={groups}
          targets={targets}
          users={users}
          onFlash={setFlash}
          onReload={loadOverview}
        />
      )}

      {activeTab === 'databases' && (
        <AdminTargetsTab
          targets={targets}
          onFlash={setFlash}
          onReload={loadOverview}
        />
      )}

      {activeTab === 'codes' && (
        <AdminCodesTab
          codes={codes}
          groups={groups}
          onFlash={setFlash}
          onReload={loadOverview}
        />
      )}
    </div>
  )
}
