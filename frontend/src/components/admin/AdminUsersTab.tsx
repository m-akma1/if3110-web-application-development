import React, { useState } from 'react'
import { api } from '../../api/client'
import { UserManagementItem, GroupItem, User } from '../../types'
import { adminInput, adminSelect, adminLabel, btnPrimary, btnSmall, btnDanger } from './adminStyles'

interface AdminUsersTabProps {
  currentUser: User | null
  users: UserManagementItem[]
  groups: GroupItem[]
  onFlash: (flash: { success: boolean; message: string } | null) => void
  onReload: () => Promise<void>
}

export const AdminUsersTab: React.FC<AdminUsersTabProps> = ({
  currentUser,
  users,
  groups,
  onFlash,
  onReload,
}) => {
  const [newUsername, setNewUsername] = useState('')
  const [newEmail, setNewEmail] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [newRole, setNewRole] = useState('USER')
  const [newGroupIDs, setNewGroupIDs] = useState<string[]>([])
  const [resetPasswords, setResetPasswords] = useState<Record<string, string>>({})

  const handleCreateUser = async (e: React.FormEvent) => {
    e.preventDefault()
    onFlash(null)
    try {
      const res = await api.createAdminUser({
        username: newUsername.trim(),
        email: newEmail.trim(),
        password: newPassword,
        role: newRole,
        group_ids: newGroupIDs,
      })
      onFlash({ success: true, message: res.message || 'User berhasil dibuat.' })
      setNewUsername('')
      setNewEmail('')
      setNewPassword('')
      setNewGroupIDs([])
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message || 'Gagal membuat user' })
    }
  }

  const handleSetRole = async (userId: string, role: string) => {
    onFlash(null)
    try {
      const res = await api.setAdminUserRole(userId, role)
      onFlash({ success: true, message: res.message })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleSetActive = async (userId: string, username: string, active: boolean) => {
    if (!active && !window.confirm(`Nonaktifkan ${username}? Semua sesinya akan langsung dicabut.`)) {
      return
    }
    onFlash(null)
    try {
      const res = await api.setAdminUserActive(userId, active)
      onFlash({ success: true, message: res.message })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleResetPassword = async (userId: string) => {
    const pw = resetPasswords[userId]
    if (!pw) return
    onFlash(null)
    try {
      const res = await api.resetAdminUserPassword(userId, pw)
      onFlash({ success: true, message: res.message })
      setResetPasswords((prev) => ({ ...prev, [userId]: '' }))
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  return (
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
                const isActor = currentUser?.id === u.id
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
  )
}
