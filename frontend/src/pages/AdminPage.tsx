import React, { useState, useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { api } from '../api/client'
import { AdminOverview, TargetItem, GroupItem } from '../types'

const adminInput = 'w-full px-3 py-2 bg-app-input border border-app-border rounded-xl text-xs text-white placeholder:text-zinc-500 focus:ring-2 focus:ring-brand-primary/50 focus:border-brand-primary focus:outline-none'
const adminSelect = 'bg-app-input border border-app-border text-zinc-200 text-xs rounded-lg px-2 py-1.5 focus:outline-none focus:border-brand-primary cursor-pointer'
const adminLabel = 'block text-xs font-medium text-zinc-300 mb-1'
const btnPrimary = 'px-3 py-2 rounded-xl bg-brand-primary hover:bg-brand-primary-hover text-app-bg text-xs font-bold transition cursor-pointer'
const btnSmall = 'px-2.5 py-1 rounded-lg bg-app-active hover:bg-app-border-strong text-zinc-200 text-[11px] font-semibold transition cursor-pointer'
const btnDanger = 'px-2.5 py-1 rounded-lg bg-sem-danger/10 hover:bg-sem-danger/20 text-sem-danger text-[11px] font-semibold border border-sem-danger/30 transition cursor-pointer'

function levelLabel(level: string): string {
  switch (level) {
    case 'VIEW_EDIT':
      return 'View + Edit'
    case 'VIEW_ONLY':
      return 'View Only'
    default:
      return 'Tanpa akses'
  }
}

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

  // --- Users Handlers ---
  const [newUsername, setNewUsername] = useState('')
  const [newEmail, setNewEmail] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newRole, setNewRole] = useState('USER')
  const [newGroupIDs, setNewGroupIDs] = useState<string[]>([])

  const handleCreateUser = async (e: React.FormEvent) => {
    e.preventDefault()
    setFlash(null)
    try {
      const res = await api.createAdminUser({
        username: newUsername.trim(),
        email: newEmail.trim(),
        password: newPassword,
        role: newRole,
        group_ids: newGroupIDs,
      })
      setFlash({ success: true, message: res.message || 'User berhasil dibuat.' })
      setNewUsername('')
      setNewEmail('')
      setNewPassword('')
      setNewGroupIDs([])
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message || 'Gagal membuat user' })
    }
  }

  const handleSetRole = async (userId: string, role: string) => {
    setFlash(null)
    try {
      const res = await api.setAdminUserRole(userId, role)
      setFlash({ success: true, message: res.message })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleSetActive = async (userId: string, username: string, active: boolean) => {
    if (!active && !window.confirm(`Nonaktifkan ${username}? Semua sesinya akan langsung dicabut.`)) {
      return
    }
    setFlash(null)
    try {
      const res = await api.setAdminUserActive(userId, active)
      setFlash({ success: true, message: res.message })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const [resetPasswords, setResetPasswords] = useState<Record<string, string>>({})
  const handleResetPassword = async (userId: string) => {
    const pw = resetPasswords[userId]
    if (!pw) return
    setFlash(null)
    try {
      const res = await api.resetAdminUserPassword(userId, pw)
      setFlash({ success: true, message: res.message })
      setResetPasswords((prev) => ({ ...prev, [userId]: '' }))
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  // --- Groups Handlers ---
  const [newGroupName, setNewGroupName] = useState('')
  const [newGroupDesc, setNewGroupDesc] = useState('')

  const handleCreateGroup = async (e: React.FormEvent) => {
    e.preventDefault()
    setFlash(null)
    try {
      const res = await api.createAdminGroup({ name: newGroupName.trim(), description: newGroupDesc.trim() })
      setFlash({ success: true, message: res.message })
      setNewGroupName('')
      setNewGroupDesc('')
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleSetGrant = async (groupId: string, databaseId: string, level: string) => {
    setFlash(null)
    try {
      const res = await api.setAdminGrant({ group_id: groupId, database_id: databaseId, level })
      setFlash({ success: true, message: res.message })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleDeleteGroup = async (g: GroupItem) => {
    if (!window.confirm(`Hapus group ${g.name}? Anggota kehilangan akses dari group ini dan kode undangannya ikut terhapus.`)) {
      return
    }
    setFlash(null)
    try {
      const res = await api.deleteAdminGroup(g.id)
      setFlash({ success: true, message: res.message })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const [editGroupData, setEditGroupData] = useState<Record<string, { name: string; description: string }>>({})
  const handleRenameGroup = async (g: GroupItem) => {
    const data = editGroupData[g.id] || { name: g.name, description: g.description }
    setFlash(null)
    try {
      const res = await api.renameAdminGroup(g.id, data)
      setFlash({ success: true, message: res.message })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const [addMemberSelect, setAddMemberSelect] = useState<Record<string, string>>({})
  const handleAddMember = async (groupId: string) => {
    const userId = addMemberSelect[groupId]
    if (!userId) return
    setFlash(null)
    try {
      const res = await api.addAdminGroupMember(groupId, userId)
      setFlash({ success: true, message: res.message })
      setAddMemberSelect((prev) => ({ ...prev, [groupId]: '' }))
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleRemoveMember = async (groupId: string, userId: string) => {
    setFlash(null)
    try {
      const res = await api.removeAdminGroupMember(groupId, userId)
      setFlash({ success: true, message: res.message })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleSetLeader = async (groupId: string, userId: string, username?: string) => {
    setFlash(null)
    try {
      const res = await api.setAdminGroupLeader(groupId, userId)
      const msg = userId
        ? (res.message || `${username || 'User'} ditetapkan sebagai Group Leader.`)
        : 'Group Leader telah dicopot.'
      setFlash({ success: true, message: msg })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message || 'Gagal menetapkan Group Leader' })
    }
  }

  // --- Databases Handlers ---
  const [targetForm, setTargetForm] = useState({
    name: '',
    host: '',
    port: 5432,
    db_name: '',
    username: '',
    password: '',
    ssl_mode: 'disable',
  })

  const handleCreateTarget = async (e: React.FormEvent) => {
    e.preventDefault()
    setFlash(null)
    try {
      const res = await api.createAdminTarget(targetForm)
      setFlash({ success: true, message: res.message })
      setTargetForm({ name: '', host: '', port: 5432, db_name: '', username: '', password: '', ssl_mode: 'disable' })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleTestTarget = async (id: string) => {
    setFlash(null)
    try {
      const res = await api.testAdminTarget(id)
      setFlash({ success: true, message: res.message })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleDeleteTarget = async (t: TargetItem) => {
    if (!window.confirm(`Hapus ${t.name} dari platform? Seluruh akses group ke database ini ikut dihapus. Database aslinya tidak diubah.`)) {
      return
    }
    setFlash(null)
    try {
      const res = await api.deleteAdminTarget(t.id)
      setFlash({ success: true, message: res.message })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const [editTargetData, setEditTargetData] = useState<Record<string, any>>({})
  const handleUpdateTarget = async (id: string) => {
    const data = editTargetData[id]
    if (!data) return
    setFlash(null)
    try {
      const res = await api.updateAdminTarget(id, data)
      setFlash({ success: true, message: res.message })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  // --- Codes Handlers ---
  const [codeGroup, setCodeGroup] = useState('')
  const [codeValue, setCodeValue] = useState('')

  const handleCreateCode = async (e: React.FormEvent) => {
    e.preventDefault()
    setFlash(null)
    try {
      const res = await api.createAdminCode({ group_id: codeGroup, code: codeValue })
      setFlash({ success: true, message: res.message })
      setCodeValue('')
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleSetCodeActive = async (code: string, active: boolean) => {
    setFlash(null)
    try {
      const res = await api.setAdminCodeActive(code, active)
      setFlash({ success: true, message: res.message })
      await loadOverview()
    } catch (err: any) {
      setFlash({ success: false, message: err.data?.error || err.message })
    }
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

      {/* USERS TAB */}
      {activeTab === 'users' && (
        <div className="space-y-6">
          {/* Card: Tambah Pengguna */}
          <div className="bg-app-card border border-app-border rounded-2xl overflow-hidden shadow-xl">
            <div className="px-4 py-3 bg-app-input border-b border-app-border flex items-center justify-between gap-3">
              <span className="text-xs font-bold text-white uppercase tracking-wider">Tambah Pengguna</span>
              <span className="text-[10px] text-zinc-400">Pengguna baru otomatis menjadi anggota Public</span>
            </div>
            <form onSubmit={handleCreateUser} className="p-4 grid grid-cols-1 md:grid-cols-4 gap-3">
              <div>
                <label className={adminLabel} htmlFor="new-username">Username</label>
                <input
                  id="new-username"
                  required
                  minLength={3}
                  maxLength={32}
                  autoComplete="off"
                  value={newUsername}
                  onChange={(e) => setNewUsername(e.target.value)}
                  className={adminInput}
                  placeholder="budi.santoso"
                />
              </div>
              <div>
                <label className={adminLabel} htmlFor="new-email">Email (opsional)</label>
                <input
                  id="new-email"
                  type="email"
                  autoComplete="off"
                  value={newEmail}
                  onChange={(e) => setNewEmail(e.target.value)}
                  className={adminInput}
                  placeholder="budi@example.com"
                />
              </div>
              <div>
                <label className={adminLabel} htmlFor="new-password">Password awal</label>
                <input
                  id="new-password"
                  type="password"
                  required
                  minLength={8}
                  autoComplete="new-password"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  className={adminInput}
                />
              </div>
              <div>
                <label className={adminLabel} htmlFor="new-role">Role</label>
                <select
                  id="new-role"
                  value={newRole}
                  onChange={(e) => setNewRole(e.target.value)}
                  className={adminInput}
                >
                  <option value="USER">USER</option>
                  <option value="PLATFORM_ADMIN">PLATFORM_ADMIN</option>
                </select>
              </div>
              <fieldset className="md:col-span-3">
                <legend className={adminLabel}>Group tambahan</legend>
                <div className="flex flex-wrap gap-3">
                  {groups.filter((g) => !g.is_system).map((g) => (
                    <label key={g.id} className="flex items-center gap-1.5 text-xs text-zinc-300">
                      <input
                        type="checkbox"
                        checked={newGroupIDs.includes(g.id)}
                        onChange={(e) => {
                          if (e.target.checked) setNewGroupIDs([...newGroupIDs, g.id])
                          else setNewGroupIDs(newGroupIDs.filter((id) => id !== g.id))
                        }}
                        className="accent-brand-primary"
                      />
                      {g.name}
                    </label>
                  ))}
                </div>
              </fieldset>
              <div className="flex items-end">
                <button type="submit" className={`${btnPrimary} w-full`}>+ Buat Pengguna</button>
              </div>
            </form>
          </div>

          {/* Card: Pengguna Platform */}
          <div className="bg-app-card border border-app-border rounded-2xl overflow-hidden shadow-xl">
            <div className="px-4 py-3 bg-app-input border-b border-app-border flex items-center justify-between gap-3">
              <span className="text-xs font-bold text-white uppercase tracking-wider">Pengguna Platform</span>
              <span className="text-[10px] text-zinc-400">Mengubah role, status, atau password langsung berlaku pada sesi pengguna</span>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm text-zinc-300">
                <thead className="bg-app-input text-xs uppercase tracking-wider text-zinc-400 border-b border-app-border">
                  <tr>
                    <th className="px-4 py-3 font-semibold">Pengguna</th>
                    <th className="px-4 py-3 font-semibold">Group</th>
                    <th className="px-4 py-3 font-semibold">Role</th>
                    <th className="px-4 py-3 font-semibold">Status</th>
                    <th className="px-4 py-3 font-semibold">Reset Password</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-app-border">
                  {users.map((u) => {
                    const isActor = user?.id === u.id
                    return (
                      <tr key={u.id} className="align-top">
                        <td className="px-4 py-3">
                          <div className="text-xs font-semibold text-zinc-100">
                            {u.username}
                            {isActor && <span className="ml-1 text-[10px] text-brand-primary">(Anda)</span>}
                          </div>
                          {u.email && <div className="text-[11px] text-zinc-500">{u.email}</div>}
                        </td>
                        <td className="px-4 py-3">
                          <div className="flex flex-wrap gap-1">
                            {u.groups?.map((g) => (
                              <span key={g.id} className="px-1.5 py-0.5 rounded text-[10px] font-mono bg-app-input border border-app-border text-zinc-300">
                                {g.name}
                              </span>
                            ))}
                          </div>
                        </td>
                        <td className="px-4 py-3">
                          {isActor ? (
                            <span className="text-xs font-mono">{u.role}</span>
                          ) : (
                            <div className="flex items-center gap-2">
                              <select
                                defaultValue={u.role}
                                aria-label={`Role ${u.username}`}
                                className={adminSelect}
                                id={`role-select-${u.id}`}
                              >
                                <option value="USER">USER</option>
                                <option value="PLATFORM_ADMIN">PLATFORM_ADMIN</option>
                              </select>
                              <button
                                type="button"
                                onClick={() => {
                                  const sel = document.getElementById(`role-select-${u.id}`) as HTMLSelectElement
                                  if (sel) handleSetRole(u.id, sel.value)
                                }}
                                className={btnSmall}
                              >
                                Simpan
                              </button>
                            </div>
                          )}
                        </td>
                        <td className="px-4 py-3">
                          {u.is_active ? (
                            <>
                              <span className="text-xs text-sem-success mr-2">Aktif</span>
                              {!isActor && (
                                <button
                                  type="button"
                                  onClick={() => handleSetActive(u.id, u.username, false)}
                                  className={btnDanger}
                                >
                                  Nonaktifkan
                                </button>
                              )}
                            </>
                          ) : (
                            <>
                              <span className="text-xs text-zinc-500 mr-2">Nonaktif</span>
                              <button
                                type="button"
                                onClick={() => handleSetActive(u.id, u.username, true)}
                                className={btnSmall}
                              >
                                Aktifkan
                              </button>
                            </>
                          )}
                        </td>
                        <td className="px-4 py-3">
                          <form
                            onSubmit={(e) => {
                              e.preventDefault()
                              handleResetPassword(u.id)
                            }}
                            className="flex items-center gap-2"
                          >
                            <input
                              type="password"
                              minLength={8}
                              placeholder="password baru"
                              autoComplete="new-password"
                              value={resetPasswords[u.id] || ''}
                              onChange={(e) => setResetPasswords({ ...resetPasswords, [u.id]: e.target.value })}
                              className="px-2 py-1 bg-app-input border border-app-border rounded-lg text-xs text-white placeholder:text-zinc-600 focus:outline-none focus:border-brand-primary w-32"
                            />
                            <button type="submit" className={btnSmall}>
                              Simpan
                            </button>
                          </form>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}

      {/* GROUPS TAB */}
      {activeTab === 'groups' && (
        <div className="space-y-6">
          {/* Matriks Akses */}
          <div className="bg-app-card border border-app-border rounded-2xl overflow-hidden shadow-xl">
            <div className="px-4 py-3 bg-app-input border-b border-app-border flex items-center justify-between gap-3">
              <span className="text-xs font-bold text-white uppercase tracking-wider">Matriks Akses Group × Database</span>
              <span className="text-[10px] text-zinc-400">Akses efektif user = akses tertinggi dari seluruh group-nya</span>
            </div>
            <div className="overflow-x-auto">
              {targets.length === 0 ? (
                <p className="p-4 text-xs text-zinc-500">Belum ada database terdaftar. Tambahkan di tab Koneksi Database.</p>
              ) : (
                <table className="w-full text-left text-sm text-zinc-300">
                  <thead className="bg-app-input text-xs uppercase tracking-wider text-zinc-400 border-b border-app-border">
                    <tr>
                      <th className="px-4 py-3 font-semibold">Group</th>
                      {targets.map((t) => (
                        <th key={t.id} className="px-4 py-3 font-semibold">{t.name}</th>
                      ))}
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-app-border">
                    {groups.map((g) => (
                      <tr key={g.id}>
                        <td className="px-4 py-3 text-xs font-semibold text-zinc-100">{g.name}</td>
                        {targets.map((t) => {
                          const lvl = g.grants?.[t.id] || 'NONE'
                          return (
                            <td key={t.id} className="px-4 py-3">
                              <select
                                value={lvl}
                                aria-label={`Akses ${g.name} pada ${t.name}`}
                                onChange={(e) => handleSetGrant(g.id, t.id, e.target.value)}
                                className={adminSelect}
                              >
                                <option value="NONE">{levelLabel('NONE')}</option>
                                <option value="VIEW_ONLY">{levelLabel('VIEW_ONLY')}</option>
                                <option value="VIEW_EDIT">{levelLabel('VIEW_EDIT')}</option>
                              </select>
                            </td>
                          )
                        })}
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>

          {/* Buat Group */}
          <div className="bg-app-card border border-app-border rounded-2xl overflow-hidden shadow-xl">
            <div className="px-4 py-3 bg-app-input border-b border-app-border">
              <span className="text-xs font-bold text-white uppercase tracking-wider">Buat Group</span>
            </div>
            <form onSubmit={handleCreateGroup} className="p-4 grid grid-cols-1 md:grid-cols-3 gap-3">
              <div>
                <label className={adminLabel} htmlFor="new-group-name">Nama group</label>
                <input
                  id="new-group-name"
                  required
                  minLength={2}
                  maxLength={64}
                  value={newGroupName}
                  onChange={(e) => setNewGroupName(e.target.value)}
                  className={adminInput}
                  placeholder="Backend Engineers"
                />
              </div>
              <div>
                <label className={adminLabel} htmlFor="new-group-desc">Deskripsi</label>
                <input
                  id="new-group-desc"
                  value={newGroupDesc}
                  onChange={(e) => setNewGroupDesc(e.target.value)}
                  className={adminInput}
                />
              </div>
              <div className="flex items-end">
                <button type="submit" className={`${btnPrimary} w-full`}>+ Buat Group</button>
              </div>
            </form>
          </div>

          {/* Group Cards Grid */}
          <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
            {groups.map((g) => {
              const members = g.members || []
              const grants = g.grants || {}
              const memberCount = g.member_count ?? members.length
              const currentLeader = members.find((m) => m.id === g.leader_id)

              return (
                <div key={g.id} className="bg-app-card border border-app-border rounded-2xl p-4 space-y-3">
                  <div className="flex items-start justify-between gap-3">
                    <div>
                      <div className="text-sm font-bold text-white">
                        {g.name}
                        {g.is_system && (
                          <span className="ml-1.5 px-1.5 py-0.5 rounded text-[10px] font-bold bg-app-active text-zinc-300">
                            SISTEM
                          </span>
                        )}
                      </div>
                      <div className="text-[11px] text-zinc-400">{g.description}</div>
                    </div>
                    {!g.is_system && (
                      <button type="button" onClick={() => handleDeleteGroup(g)} className={btnDanger}>
                        Hapus
                      </button>
                    )}
                  </div>

                  <details className="text-xs">
                    <summary className="cursor-pointer text-zinc-400 hover:text-zinc-200">Ubah nama / deskripsi</summary>
                    <div className="mt-2 grid grid-cols-1 md:grid-cols-3 gap-2">
                      {!g.is_system && (
                        <input
                          placeholder="Nama group"
                          defaultValue={g.name}
                          onChange={(e) =>
                            setEditGroupData({
                              ...editGroupData,
                              [g.id]: { ...(editGroupData[g.id] || { name: g.name, description: g.description }), name: e.target.value },
                            })
                          }
                          className={adminInput}
                        />
                      )}
                      <input
                        placeholder="Deskripsi"
                        defaultValue={g.description}
                        onChange={(e) =>
                          setEditGroupData({
                            ...editGroupData,
                            [g.id]: { ...(editGroupData[g.id] || { name: g.name, description: g.description }), description: e.target.value },
                          })
                        }
                        className={adminInput}
                      />
                      <button type="button" onClick={() => handleRenameGroup(g)} className={btnSmall}>
                        Simpan
                      </button>
                    </div>
                  </details>

                  {/* Group Leader Selector */}
                  {!g.is_system && (
                    <div className="flex flex-wrap items-center justify-between gap-2 p-2.5 rounded-xl bg-app-input border border-app-border">
                      <div className="flex items-center gap-2">
                        <span className="text-[10px] font-bold text-zinc-400 uppercase tracking-wider">
                          Group Leader:
                        </span>
                        {currentLeader ? (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-brand-primary text-app-bg uppercase tracking-wider">
                            LEADER: {currentLeader.username}
                          </span>
                        ) : (
                          <span className="text-xs text-zinc-500 italic">Belum ada leader</span>
                        )}
                      </div>

                      {members.length > 0 && (
                        <div className="flex items-center gap-1.5">
                          <select
                            value={g.leader_id || ''}
                            onChange={(e) => {
                              const selectedId = e.target.value
                              const selectedMember = members.find((m) => m.id === selectedId)
                              handleSetLeader(g.id, selectedId, selectedMember?.username)
                            }}
                            className="text-xs bg-app-card border border-app-border rounded-lg px-2.5 py-1 text-zinc-200 cursor-pointer focus:ring-1 focus:ring-brand-primary"
                          >
                            <option value="">{g.leader_id ? 'Pilih / Ganti Leader…' : 'Tetapkan Leader…'}</option>
                            {members.map((m) => (
                              <option key={m.id} value={m.id}>
                                {m.username}
                              </option>
                            ))}
                          </select>
                        </div>
                      )}
                    </div>
                  )}

                  {/* Anggota Section */}
                  <div className="pt-2 border-t border-app-border space-y-2">
                    <div className="flex items-center justify-between">
                      <span className="text-[10px] font-bold text-zinc-400 uppercase tracking-wider">
                        Anggota ({memberCount})
                      </span>
                      {g.is_system && <span className="text-[10px] text-zinc-500">Semua user otomatis menjadi anggota</span>}
                    </div>
                    {!g.is_system && (
                      <div className="flex flex-wrap gap-1.5">
                        {members.length === 0 ? (
                          <span className="text-[11px] text-zinc-500">Belum ada anggota.</span>
                        ) : (
                          members.map((m) => {
                            const isLeader = Boolean(g.leader_id && g.leader_id === m.id)
                            return (
                              <span
                                key={m.id}
                                className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-lg text-xs border ${
                                  isLeader
                                    ? 'bg-brand-primary/15 border-brand-primary/40 text-brand-primary font-semibold'
                                    : 'bg-app-input border-app-border text-zinc-300'
                                }`}
                              >
                                {isLeader && (
                                  <span className="text-[9px] px-1.5 py-0.2 rounded bg-brand-primary text-app-bg font-bold tracking-wider">
                                    LEADER
                                  </span>
                                )}
                                <span>{m.username}</span>
                                <button
                                  type="button"
                                  onClick={() => handleRemoveMember(g.id, m.id)}
                                  className="text-zinc-500 hover:text-sem-danger font-bold text-xs cursor-pointer ml-0.5"
                                  title="Keluarkan dari group"
                                >
                                  ×
                                </button>
                              </span>
                            )
                          })
                        )}
                      </div>
                    )}

                    {!g.is_system && (
                      <div className="pt-1 flex items-center gap-2">
                        <select
                          value={addMemberSelect[g.id] || ''}
                          onChange={(e) => setAddMemberSelect({ ...addMemberSelect, [g.id]: e.target.value })}
                          className={`${adminSelect} text-xs flex-1`}
                        >
                          <option value="">Tambah anggota…</option>
                          {users
                            .filter((u) => !members.some((m) => m.id === u.id))
                            .map((u) => (
                              <option key={u.id} value={u.id}>
                                {u.username}
                              </option>
                            ))}
                        </select>
                        <button
                          type="button"
                          onClick={() => handleAddMember(g.id)}
                          disabled={!addMemberSelect[g.id]}
                          className={`${btnSmall} disabled:opacity-40`}
                        >
                          + Tambah
                        </button>
                      </div>
                    )}
                  </div>
                </div>
              )
            })}
          </div>
        </div>
      )}

      {/* DATABASES TAB */}
      {activeTab === 'databases' && (
        <div className="space-y-6">
          {/* Daftarkan Database */}
          <div className="bg-app-card border border-app-border rounded-2xl overflow-hidden shadow-xl">
            <div className="px-4 py-3 bg-app-input border-b border-app-border flex items-center justify-between gap-3">
              <span className="text-xs font-bold text-white uppercase tracking-wider">Daftarkan Database PostgreSQL</span>
              <span className="text-[10px] text-zinc-400">Koneksi diuji saat disimpan; password disimpan terenkripsi</span>
            </div>
            <form onSubmit={handleCreateTarget} className="p-4">
              <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                <div>
                  <label className={adminLabel}>Nama tampilan</label>
                  <input
                    value={targetForm.name}
                    onChange={(e) => setTargetForm({ ...targetForm, name: e.target.value })}
                    required
                    minLength={2}
                    maxLength={100}
                    placeholder="Postgres Staging"
                    className={adminInput}
                  />
                </div>
                <div>
                  <label className={adminLabel}>Host</label>
                  <input
                    value={targetForm.host}
                    onChange={(e) => setTargetForm({ ...targetForm, host: e.target.value })}
                    required
                    placeholder="target-postgres"
                    className={adminInput}
                  />
                </div>
                <div>
                  <label className={adminLabel}>Port</label>
                  <input
                    type="number"
                    min={1}
                    max={65535}
                    value={targetForm.port}
                    onChange={(e) => setTargetForm({ ...targetForm, port: parseInt(e.target.value, 10) || 5432 })}
                    className={adminInput}
                  />
                </div>
                <div>
                  <label className={adminLabel}>Nama database</label>
                  <input
                    value={targetForm.db_name}
                    onChange={(e) => setTargetForm({ ...targetForm, db_name: e.target.value })}
                    required
                    maxLength={63}
                    placeholder="app_db"
                    className={adminInput}
                  />
                </div>
                <div>
                  <label className={adminLabel}>Username</label>
                  <input
                    value={targetForm.username}
                    onChange={(e) => setTargetForm({ ...targetForm, username: e.target.value })}
                    required
                    placeholder="app_user"
                    className={adminInput}
                  />
                </div>
                <div>
                  <label className={adminLabel}>Password</label>
                  <input
                    type="password"
                    value={targetForm.password}
                    onChange={(e) => setTargetForm({ ...targetForm, password: e.target.value })}
                    required
                    autoComplete="new-password"
                    className={adminInput}
                  />
                </div>
                <div>
                  <label className={adminLabel}>SSL Mode</label>
                  <select
                    value={targetForm.ssl_mode}
                    onChange={(e) => setTargetForm({ ...targetForm, ssl_mode: e.target.value })}
                    className={adminInput}
                  >
                    <option value="disable">disable</option>
                    <option value="require">require</option>
                    <option value="verify-full">verify-full</option>
                  </select>
                </div>
              </div>
              <div className="mt-3 flex justify-end">
                <button type="submit" className={btnPrimary}>+ Uji & Simpan Koneksi</button>
              </div>
            </form>
          </div>

          {/* Database Terdaftar */}
          <div className="bg-app-card border border-app-border rounded-2xl overflow-hidden shadow-xl">
            <div className="px-4 py-3 bg-app-input border-b border-app-border flex items-center justify-between gap-3">
              <span className="text-xs font-bold text-white uppercase tracking-wider">Database Terdaftar</span>
              <span className="text-[10px] text-zinc-400">{targets.length} database</span>
            </div>
            {targets.length === 0 ? (
              <p className="p-4 text-xs text-zinc-500">Belum ada database terdaftar.</p>
            ) : (
              <div className="divide-y divide-app-border">
                {targets.map((t) => (
                  <div key={t.id} className="p-4 space-y-2">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <div>
                        <div className="text-sm font-bold text-white">{t.name}</div>
                        <div className="text-[11px] font-mono text-zinc-400">
                          {`${t.username}@${t.host}:${t.port}/${t.db_name} · sslmode=${t.ssl_mode}`}
                        </div>
                      </div>
                      <div className="flex items-center gap-2">
                        {t.status === 'CONNECTED' ? (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-success/15 text-sem-success border border-sem-success/30">
                            CONNECTED
                          </span>
                        ) : t.status === 'ERROR' ? (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-danger/15 text-sem-danger border border-sem-danger/30">
                            ERROR
                          </span>
                        ) : (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-app-active text-zinc-300">
                            BELUM DIUJI
                          </span>
                        )}
                        <button type="button" onClick={() => handleTestTarget(t.id)} className={btnSmall}>
                          Uji Koneksi
                        </button>
                        <button type="button" onClick={() => handleDeleteTarget(t)} className={btnDanger}>
                          Hapus
                        </button>
                      </div>
                    </div>

                    {t.last_error && (
                      <div className="text-[11px] text-sem-danger font-mono break-all">{t.last_error}</div>
                    )}
                    {t.last_checked_at && (
                      <div className="text-[10px] text-zinc-500">
                        Terakhir diuji {new Date(t.last_checked_at).toLocaleString('sv-SE')}
                      </div>
                    )}

                    <details className="text-xs">
                      <summary className="cursor-pointer text-zinc-400 hover:text-zinc-200">Ubah koneksi</summary>
                      <form
                        onSubmit={(e) => {
                          e.preventDefault()
                          handleUpdateTarget(t.id)
                        }}
                        className="mt-2"
                      >
                        <div className="grid grid-cols-1 md:grid-cols-4 gap-3">
                          <div>
                            <label className={adminLabel}>Nama tampilan</label>
                            <input
                              defaultValue={t.name}
                              onChange={(e) =>
                                setEditTargetData({
                                  ...editTargetData,
                                  [t.id]: { ...(editTargetData[t.id] || t), name: e.target.value },
                                })
                              }
                              className={adminInput}
                            />
                          </div>
                          <div>
                            <label className={adminLabel}>Host</label>
                            <input
                              defaultValue={t.host}
                              onChange={(e) =>
                                setEditTargetData({
                                  ...editTargetData,
                                  [t.id]: { ...(editTargetData[t.id] || t), host: e.target.value },
                                })
                              }
                              className={adminInput}
                            />
                          </div>
                          <div>
                            <label className={adminLabel}>Port</label>
                            <input
                              type="number"
                              defaultValue={t.port}
                              onChange={(e) =>
                                setEditTargetData({
                                  ...editTargetData,
                                  [t.id]: { ...(editTargetData[t.id] || t), port: parseInt(e.target.value, 10) },
                                })
                              }
                              className={adminInput}
                            />
                          </div>
                          <div>
                            <label className={adminLabel}>Nama database</label>
                            <input
                              defaultValue={t.db_name}
                              onChange={(e) =>
                                setEditTargetData({
                                  ...editTargetData,
                                  [t.id]: { ...(editTargetData[t.id] || t), db_name: e.target.value },
                                })
                              }
                              className={adminInput}
                            />
                          </div>
                          <div>
                            <label className={adminLabel}>Username</label>
                            <input
                              defaultValue={t.username}
                              onChange={(e) =>
                                setEditTargetData({
                                  ...editTargetData,
                                  [t.id]: { ...(editTargetData[t.id] || t), username: e.target.value },
                                })
                              }
                              className={adminInput}
                            />
                          </div>
                          <div>
                            <label className={adminLabel}>Password (kosongkan jika tidak diubah)</label>
                            <input
                              type="password"
                              placeholder="••••••••••"
                              onChange={(e) =>
                                setEditTargetData({
                                  ...editTargetData,
                                  [t.id]: { ...(editTargetData[t.id] || t), password: e.target.value },
                                })
                              }
                              className={adminInput}
                            />
                          </div>
                          <div>
                            <label className={adminLabel}>SSL Mode</label>
                            <select
                              defaultValue={t.ssl_mode}
                              onChange={(e) =>
                                setEditTargetData({
                                  ...editTargetData,
                                  [t.id]: { ...(editTargetData[t.id] || t), ssl_mode: e.target.value },
                                })
                              }
                              className={adminInput}
                            >
                              <option value="disable">disable</option>
                              <option value="require">require</option>
                              <option value="verify-full">verify-full</option>
                            </select>
                          </div>
                        </div>
                        <div className="mt-3 flex justify-end">
                          <button type="submit" className={btnSmall}>Simpan & Uji Ulang</button>
                        </div>
                      </form>
                    </details>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}

      {/* CODES TAB */}
      {activeTab === 'codes' && (
        <div className="space-y-6">
          <div className="bg-app-card border border-app-border rounded-2xl overflow-hidden shadow-xl">
            <div className="px-4 py-3 bg-app-input border-b border-app-border flex items-center justify-between gap-3">
              <span className="text-xs font-bold text-white uppercase tracking-wider">Buat Kode Undangan</span>
              <span className="text-[10px] text-zinc-400">Pengguna yang mendaftar dengan kode ini masuk ke group tujuan</span>
            </div>
            <form onSubmit={handleCreateCode} className="p-4 grid grid-cols-1 md:grid-cols-3 gap-3">
              <div>
                <label className={adminLabel} htmlFor="code-group">Group tujuan</label>
                <select
                  id="code-group"
                  required
                  value={codeGroup}
                  onChange={(e) => setCodeGroup(e.target.value)}
                  className={adminInput}
                >
                  <option value="">Pilih group…</option>
                  {groups.filter((g) => !g.is_system).map((g) => (
                    <option key={g.id} value={g.id}>
                      {g.name}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className={adminLabel} htmlFor="code-value">Kode (opsional)</label>
                <input
                  id="code-value"
                  maxLength={64}
                  placeholder="kosongkan untuk kode acak"
                  value={codeValue}
                  onChange={(e) => setCodeValue(e.target.value.toUpperCase())}
                  className={`${adminInput} font-mono uppercase`}
                />
              </div>
              <div className="flex items-end">
                <button type="submit" className={`${btnPrimary} w-full`}>+ Buat Kode</button>
              </div>
            </form>
          </div>

          <div className="bg-app-card border border-app-border rounded-2xl overflow-hidden shadow-xl">
            <div className="px-4 py-3 bg-app-input border-b border-app-border">
              <span className="text-xs font-bold text-white uppercase tracking-wider">Kode Undangan</span>
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm text-zinc-300">
                <thead className="bg-app-input text-xs uppercase tracking-wider text-zinc-400 border-b border-app-border">
                  <tr>
                    <th className="px-4 py-3 font-semibold">Kode</th>
                    <th className="px-4 py-3 font-semibold">Group Tujuan</th>
                    <th className="px-4 py-3 font-semibold">Dibuat</th>
                    <th className="px-4 py-3 font-semibold">Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-app-border">
                  {codes.length === 0 ? (
                    <tr>
                      <td colSpan={4} className="px-4 py-8 text-center text-xs text-zinc-500">
                        Belum ada kode undangan.
                      </td>
                    </tr>
                  ) : (
                    codes.map((c) => (
                      <tr key={c.code}>
                        <td className="px-4 py-3 text-xs font-mono font-bold text-brand-primary">{c.code}</td>
                        <td className="px-4 py-3 text-xs">{c.group_name}</td>
                        <td className="px-4 py-3 text-xs text-zinc-400">
                          {new Date(c.created_at).toLocaleString('sv-SE', {
                            year: 'numeric',
                            month: '2-digit',
                            day: '2-digit',
                            hour: '2-digit',
                            minute: '2-digit',
                          })}
                        </td>
                        <td className="px-4 py-3 text-xs">
                          {c.is_active ? (
                            <>
                              <span className="text-sem-success mr-2">Aktif</span>
                              <button
                                type="button"
                                onClick={() => handleSetCodeActive(c.code, false)}
                                className={btnDanger}
                              >
                                Nonaktifkan
                              </button>
                            </>
                          ) : (
                            <>
                              <span className="text-zinc-500 mr-2">Nonaktif</span>
                              <button
                                type="button"
                                onClick={() => handleSetCodeActive(c.code, true)}
                                className={btnSmall}
                              >
                                Aktifkan
                              </button>
                            </>
                          )}
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
