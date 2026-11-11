import React, { useState } from 'react'
import { api } from '../../api/client'
import { GroupItem, TargetItem, UserManagementItem } from '../../types'
import { adminInput, adminSelect, adminLabel, btnPrimary, btnSmall, btnDanger, levelLabel } from './adminStyles'

interface AdminGroupsTabProps {
  groups: GroupItem[]
  targets: TargetItem[]
  users: UserManagementItem[]
  onFlash: (flash: { success: boolean; message: string } | null) => void
  onReload: () => Promise<void>
}

export const AdminGroupsTab: React.FC<AdminGroupsTabProps> = ({
  groups,
  targets,
  users,
  onFlash,
  onReload,
}) => {
  const [newGroupName, setNewGroupName] = useState('')
  const [newGroupDesc, setNewGroupDesc] = useState('')
  const [editGroupData, setEditGroupData] = useState<Record<string, { name: string; description: string }>>({})
  const [addMemberSelect, setAddMemberSelect] = useState<Record<string, string>>({})

  const handleCreateGroup = async (e: React.FormEvent) => {
    e.preventDefault()
    onFlash(null)
    try {
      const res = await api.createAdminGroup({ name: newGroupName.trim(), description: newGroupDesc.trim() })
      onFlash({ success: true, message: res.message })
      setNewGroupName('')
      setNewGroupDesc('')
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleSetGrant = async (groupId: string, databaseId: string, level: string) => {
    onFlash(null)
    try {
      const res = await api.setAdminGrant({ group_id: groupId, database_id: databaseId, level })
      onFlash({ success: true, message: res.message })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleDeleteGroup = async (g: GroupItem) => {
    if (!window.confirm(`Hapus group ${g.name}? Anggota kehilangan akses dari group ini dan kode undangannya ikut terhapus.`)) {
      return
    }
    onFlash(null)
    try {
      const res = await api.deleteAdminGroup(g.id)
      onFlash({ success: true, message: res.message })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleRenameGroup = async (g: GroupItem) => {
    const data = editGroupData[g.id] || { name: g.name, description: g.description }
    onFlash(null)
    try {
      const res = await api.renameAdminGroup(g.id, data)
      onFlash({ success: true, message: res.message })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleAddMember = async (groupId: string) => {
    const userId = addMemberSelect[groupId]
    if (!userId) return
    onFlash(null)
    try {
      const res = await api.addAdminGroupMember(groupId, userId)
      onFlash({ success: true, message: res.message })
      setAddMemberSelect((prev) => ({ ...prev, [groupId]: '' }))
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleRemoveMember = async (groupId: string, userId: string) => {
    onFlash(null)
    try {
      const res = await api.removeAdminGroupMember(groupId, userId)
      onFlash({ success: true, message: res.message })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleSetLeader = async (groupId: string, userId: string, username?: string) => {
    onFlash(null)
    try {
      const res = await api.setAdminGroupLeader(groupId, userId)
      const msg = userId
        ? (res.message || `${username || 'User'} ditetapkan sebagai Group Leader.`)
        : 'Group Leader telah dicopot.'
      onFlash({ success: true, message: msg })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message || 'Gagal menetapkan Group Leader' })
    }
  }

  return (
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
  )
}
