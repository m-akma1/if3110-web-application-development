import React, { useState } from 'react'
import { api } from '../../api/client'
import { InviteCodeItem, GroupItem } from '../../types'
import { adminInput, adminLabel, btnPrimary, btnSmall, btnDanger } from './adminStyles'

interface AdminCodesTabProps {
  codes: InviteCodeItem[]
  groups: GroupItem[]
  onFlash: (flash: { success: boolean; message: string } | null) => void
  onReload: () => Promise<void>
}

export const AdminCodesTab: React.FC<AdminCodesTabProps> = ({
  codes,
  groups,
  onFlash,
  onReload,
}) => {
  const [codeGroup, setCodeGroup] = useState('')
  const [codeValue, setCodeValue] = useState('')

  const handleCreateCode = async (e: React.FormEvent) => {
    e.preventDefault()
    onFlash(null)
    try {
      const res = await api.createAdminCode({ group_id: codeGroup, code: codeValue })
      onFlash({ success: true, message: res.message })
      setCodeValue('')
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleSetCodeActive = async (code: string, active: boolean) => {
    onFlash(null)
    try {
      const res = await api.setAdminCodeActive(code, active)
      onFlash({ success: true, message: res.message })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  return (
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
  )
}
