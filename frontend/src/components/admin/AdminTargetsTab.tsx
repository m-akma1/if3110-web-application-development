import React, { useState } from 'react'
import { api } from '../../api/client'
import { TargetItem } from '../../types'
import { adminInput, adminLabel, btnPrimary, btnSmall, btnDanger } from './adminStyles'

interface AdminTargetsTabProps {
  targets: TargetItem[]
  onFlash: (flash: { success: boolean; message: string } | null) => void
  onReload: () => Promise<void>
}

export const AdminTargetsTab: React.FC<AdminTargetsTabProps> = ({
  targets,
  onFlash,
  onReload,
}) => {
  const [targetForm, setTargetForm] = useState({
    name: '',
    host: '',
    port: 5432,
    db_name: '',
    username: '',
    password: '',
    ssl_mode: 'disable',
  })
  const [editTargetData, setEditTargetData] = useState<Record<string, any>>({})

  const handleCreateTarget = async (e: React.FormEvent) => {
    e.preventDefault()
    onFlash(null)
    try {
      const res = await api.createAdminTarget(targetForm)
      onFlash({ success: true, message: res.message })
      setTargetForm({ name: '', host: '', port: 5432, db_name: '', username: '', password: '', ssl_mode: 'disable' })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleTestTarget = async (id: string) => {
    onFlash(null)
    try {
      const res = await api.testAdminTarget(id)
      onFlash({ success: true, message: res.message })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleDeleteTarget = async (t: TargetItem) => {
    if (!window.confirm(`Hapus ${t.name} dari platform? Seluruh akses group ke database ini ikut dihapus. Database aslinya tidak diubah.`)) {
      return
    }
    onFlash(null)
    try {
      const res = await api.deleteAdminTarget(t.id)
      onFlash({ success: true, message: res.message })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  const handleUpdateTarget = async (id: string) => {
    const data = editTargetData[id]
    if (!data) return
    onFlash(null)
    try {
      const res = await api.updateAdminTarget(id, data)
      onFlash({ success: true, message: res.message })
      await onReload()
    } catch (err: any) {
      onFlash({ success: false, message: err.data?.error || err.message })
    }
  }

  return (
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
  )
}
