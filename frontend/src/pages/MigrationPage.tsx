import React, { useState, useEffect } from 'react'
import { Link, useSearchParams, useNavigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { api } from '../api/client'
import { MigrationView, DryRunView } from '../types'
import { DryRunReport } from '../components/DryRunReport'
import { NoDatabaseAccess } from '../components/NoDatabaseAccess'

function fmtTime(t?: string): string {
  if (!t || t.startsWith('0001-01-01')) {
    return '-'
  }
  const date = new Date(t)
  if (isNaN(date.getTime())) return t
  const yyyy = date.getFullYear()
  const mm = String(date.getMonth() + 1).padStart(2, '0')
  const dd = String(date.getDate()).padStart(2, '0')
  const hh = String(date.getHours()).padStart(2, '0')
  const min = String(date.getMinutes()).padStart(2, '0')
  const ss = String(date.getSeconds()).padStart(2, '0')
  return `${yyyy}-${mm}-${dd} ${hh}:${min}:${ss}`
}

function versionLabel(v: number): string {
  return v > 0 ? `v${v}` : '-'
}

export const MigrationPage: React.FC = () => {
  const { user, groups, databases, activeDBID } = useAuth()
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()

  const draftParam = searchParams.get('draft') || ''

  const [loading, setLoading] = useState(true)
  const [canEdit, setCanEdit] = useState(false)
  const [dbName, setDbName] = useState('')
  const [drafts, setDrafts] = useState<MigrationView[]>([])
  const [history, setHistory] = useState<MigrationView[]>([])

  const [selectedDraft, setSelectedDraft] = useState<MigrationView | null>(null)
  const [name, setName] = useState('')
  const [sqlScript, setSqlScript] = useState('')

  const [feedback, setFeedback] = useState<{ success: boolean; message: string } | null>(null)
  const [dryRunReport, setDryRunReport] = useState<DryRunView | null>(null)
  const [actionLoading, setActionLoading] = useState(false)

  const fetchOverview = async (dbId: string, currentDraftId?: string) => {
    setLoading(true)
    try {
      const res = await api.getMigrationOverview(dbId, currentDraftId)
      setCanEdit(res.can_edit)
      setDbName(res.database_name || '')
      setDrafts(res.drafts || [])
      setHistory(res.history || [])

      if (currentDraftId) {
        const found = (res.drafts || []).find((d: MigrationView) => d.id === currentDraftId)
        if (found) {
          setSelectedDraft(found)
          setName(found.name)
          setSqlScript(found.sql)
        } else {
          setSelectedDraft(null)
          setName('')
          setSqlScript('')
        }
      } else {
        setSelectedDraft(null)
        setName('')
        setSqlScript('')
      }
    } catch (err: any) {
      // handled
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (activeDBID) {
      fetchOverview(activeDBID, draftParam)
    } else {
      setLoading(false)
    }
    setFeedback(null)
    setDryRunReport(null)
  }, [activeDBID, draftParam])

  const handleSelectDraft = (d: MigrationView | null) => {
    setFeedback(null)
    setDryRunReport(null)
    if (d) {
      setSearchParams({ draft: d.id })
    } else {
      setSearchParams({})
    }
  }

  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = (evt) => {
      const text = evt.target?.result as string
      setSqlScript(text)
      if (!name) {
        setName(file.name.replace(/\.sql$/i, ''))
      }
    }
    reader.readAsText(file)
  }

  const handleSaveDraft = async () => {
    if (!name.trim()) {
      setFeedback({ success: false, message: 'Nama migration wajib diisi' })
      return
    }
    setActionLoading(true)
    setFeedback(null)
    try {
      if (!selectedDraft) {
        const res = await api.createMigrationDraft({
          db_id: activeDBID,
          name: name.trim(),
          sql_script: sqlScript,
        })
        setFeedback({ success: true, message: 'Draft berhasil dibuat.' })
        await fetchOverview(activeDBID, res.draft.id)
        setSearchParams({ draft: res.draft.id })
      } else {
        const res = await api.saveMigrationDraft(selectedDraft.id, {
          name: name.trim(),
          sql_script: sqlScript,
        })
        setFeedback({ success: true, message: res.message || 'Draft disimpan.' })
        await fetchOverview(activeDBID, selectedDraft.id)
      }
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menyimpan draft' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleDeleteDraft = async () => {
    if (!selectedDraft) return
    if (!window.confirm('Hapus draft ini?')) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.deleteMigrationDraft(selectedDraft.id)
      setFeedback({ success: true, message: 'Draft berhasil dihapus.' })
      handleSelectDraft(null)
      await fetchOverview(activeDBID)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menghapus draft' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleDryRun = async () => {
    if (!selectedDraft) return
    setActionLoading(true)
    setFeedback(null)
    setDryRunReport(null)
    try {
      const res = await api.dryRunMigration(selectedDraft.id, {
        name: name.trim(),
        sql_script: sqlScript,
      })
      setDryRunReport(res)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Dry run gagal' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleExecute = async () => {
    if (!selectedDraft) return
    const ok = window.confirm(
      'Simpan dan eksekusi migration ini pada database target? Hasilnya (COMMIT atau ROLLBACK) dicatat permanen di riwayat.'
    )
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      const res = await api.executeMigration(selectedDraft.id, {
        name: name.trim(),
        sql_script: sqlScript,
      })
      navigate(`/migration/${res.migration.id}`)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Eksekusi migration gagal' })
    } finally {
      setActionLoading(false)
    }
  }

  if (!activeDBID) {
    return <NoDatabaseAccess groups={groups} />
  }

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-zinc-100">Migration Editor</h1>
          <p className="text-zinc-400 mt-1 text-sm">
            Setiap script dijalankan dalam satu transaksi: COMMIT jika semua statement berhasil, ROLLBACK jika ada yang gagal.
          </p>
        </div>
        {dbName && (
          <span className="px-2.5 py-1 rounded-lg text-xs font-mono bg-app-input text-zinc-300 border border-app-border">
            Database: {dbName}
          </span>
        )}
      </div>

      {canEdit ? (
        <div className="grid grid-cols-1 lg:grid-cols-[16rem_1fr] gap-4">
          {/* Drafts list */}
          <div className="bg-app-card border border-app-border rounded-xl p-3 space-y-2 h-fit">
            <div className="flex items-center justify-between px-1">
              <h3 className="text-xs font-bold text-zinc-400 uppercase tracking-wider">Draft</h3>
              <button
                type="button"
                onClick={() => handleSelectDraft(null)}
                className="text-[11px] font-semibold text-brand-primary hover:underline cursor-pointer"
              >
                + Draft baru
              </button>
            </div>
            {drafts.length === 0 ? (
              <p className="px-1 py-2 text-[11px] text-zinc-500">Belum ada draft.</p>
            ) : (
              drafts.map((d) => {
                const isSelected = selectedDraft?.id === d.id
                return (
                  <button
                    key={d.id}
                    type="button"
                    onClick={() => handleSelectDraft(d)}
                    className={`w-full text-left block px-3 py-2 rounded-lg border text-xs transition cursor-pointer ${
                      isSelected
                        ? 'bg-app-active border-app-border-strong text-zinc-100'
                        : 'border-transparent text-zinc-300 hover:bg-app-hover'
                    }`}
                  >
                    <div className="font-semibold truncate">{d.name}</div>
                    <div className="text-[10px] text-zinc-500 mt-0.5">
                      {d.created_by_name || '-'} · {fmtTime(d.updated_at)}
                    </div>
                  </button>
                )
              })
            )}
          </div>

          {/* Migration Editor */}
          <div id="migration-editor" className="bg-app-card border border-app-border rounded-xl p-5 space-y-4 shadow-xl">
            <form onSubmit={(e) => e.preventDefault()} className="space-y-3">
              <div className="flex flex-wrap items-center gap-3">
                <input
                  type="text"
                  name="name"
                  required
                  maxLength={200}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="Nama migration, mis. tambah tabel orders"
                  className="flex-1 min-w-[12rem] px-3 py-2 bg-app-bg border border-app-border rounded-lg text-xs text-zinc-100 focus:outline-none focus:ring-2 focus:ring-brand-primary/50"
                />
                <label className="px-3 py-2 rounded-lg text-xs font-medium bg-app-active hover:bg-app-border-strong text-zinc-200 cursor-pointer transition">
                  Muat file .sql
                  <input
                    type="file"
                    accept=".sql,text/plain"
                    className="hidden"
                    onChange={handleFileUpload}
                  />
                </label>
              </div>

              <textarea
                id="sql_script"
                name="sql_script"
                rows={12}
                spellCheck="false"
                value={sqlScript}
                onChange={(e) => setSqlScript(e.target.value)}
                placeholder="-- Tulis perintah SQL di sini. Jangan tulis BEGIN/COMMIT: platform yang mengelola transaksi."
                className="w-full p-4 bg-app-bg border border-app-border rounded-lg font-mono text-xs text-sem-info focus:outline-none focus:ring-2 focus:ring-brand-primary/50 leading-relaxed resize-y"
              ></textarea>

              <div className="flex flex-wrap items-center justify-between gap-2">
                {!selectedDraft ? (
                  <>
                    <span className="text-[11px] text-zinc-500">
                      Simpan sebagai draft dulu, lalu jalankan Dry Run sebelum eksekusi.
                    </span>
                    <button
                      type="button"
                      disabled={actionLoading}
                      onClick={handleSaveDraft}
                      className="px-4 py-1.5 rounded-lg text-xs font-bold bg-brand-primary hover:bg-brand-primary-hover text-app-bg transition shadow-md cursor-pointer disabled:opacity-50"
                    >
                      {actionLoading ? 'Menyimpan...' : 'Simpan Draft'}
                    </button>
                  </>
                ) : (
                  <>
                    <button
                      type="button"
                      disabled={actionLoading}
                      onClick={handleDeleteDraft}
                      className="px-3 py-1.5 rounded-lg text-xs font-medium text-sem-danger hover:bg-sem-danger/10 transition cursor-pointer"
                    >
                      Hapus Draft
                    </button>
                    <div className="flex flex-wrap gap-2">
                      <button
                        type="button"
                        disabled={actionLoading}
                        onClick={handleSaveDraft}
                        className="px-3 py-1.5 rounded-lg text-xs font-medium bg-app-active hover:bg-app-border-strong text-white transition cursor-pointer"
                      >
                        Simpan
                      </button>
                      <button
                        type="button"
                        disabled={actionLoading}
                        onClick={handleDryRun}
                        className="px-3 py-1.5 rounded-lg text-xs font-medium bg-app-active hover:bg-app-border-strong text-white transition cursor-pointer"
                      >
                        Dry Run + EXPLAIN
                      </button>
                      <button
                        type="button"
                        disabled={actionLoading}
                        onClick={handleExecute}
                        className="px-4 py-1.5 rounded-lg text-xs font-bold bg-brand-primary hover:bg-brand-primary-hover text-app-bg transition shadow-md cursor-pointer"
                      >
                        Eksekusi Migration
                      </button>
                    </div>
                  </>
                )}
              </div>
            </form>

            {feedback && (
              <div
                className={`p-3 rounded-lg text-xs font-mono whitespace-pre-wrap border ${
                  feedback.success
                    ? 'bg-sem-success/10 border-sem-success/30 text-sem-success'
                    : 'bg-sem-danger/10 border-sem-danger/30 text-sem-danger'
                }`}
              >
                {feedback.success ? `✓ ${feedback.message}` : `✗ ${feedback.message}`}
              </div>
            )}

            {dryRunReport && <DryRunReport report={dryRunReport} />}
          </div>
        </div>
      ) : (
        <div className="p-4 rounded-xl bg-app-input border border-app-border text-xs text-zinc-400">
          Anda memiliki akses <span className="font-semibold text-zinc-200">View Only</span> pada database ini: riwayat migrasi dapat dilihat, tetapi membuat dan menjalankan migrasi membutuhkan akses View + Edit melalui group.
        </div>
      )}

      {/* Migration History Table */}
      <div className="space-y-3">
        <h3 className="text-xs font-bold text-zinc-400 uppercase tracking-wider px-1">
          Riwayat Eksekusi Migrasi
        </h3>
        <div className="bg-app-card border border-app-border rounded-xl overflow-hidden shadow-xl">
          <table className="w-full text-left text-sm text-zinc-300">
            <thead className="bg-app-bg text-xs uppercase tracking-wider text-zinc-400 border-b border-app-border">
              <tr>
                <th className="px-4 py-3 font-semibold">Versi</th>
                <th className="px-4 py-3 font-semibold">Nama</th>
                <th className="px-4 py-3 font-semibold">Waktu Eksekusi</th>
                <th className="px-4 py-3 font-semibold">Oleh</th>
                <th className="px-4 py-3 font-semibold">Durasi</th>
                <th className="px-4 py-3 font-semibold">Status</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-app-border">
              {history.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-8 text-center text-zinc-500 text-xs">
                    Belum ada riwayat migrasi pada database ini.
                  </td>
                </tr>
              ) : (
                history.map((h) => (
                  <tr key={h.id} className="hover:bg-app-active/50 transition">
                    <td className="px-4 py-3 font-mono text-xs">{versionLabel(h.version)}</td>
                    <td className="px-4 py-3 text-xs font-semibold">
                      <Link to={`/migration/${h.id}`} className="text-zinc-200 hover:text-brand-primary hover:underline">
                        {h.name}
                      </Link>
                    </td>
                    <td className="px-4 py-3 text-xs">{fmtTime(h.executed_at)}</td>
                    <td className="px-4 py-3 text-xs">{h.executed_by_name || '-'}</td>
                    <td className="px-4 py-3 font-mono text-xs">{`${h.duration_ms} ms`}</td>
                    <td className="px-4 py-3 text-xs">
                      {h.status === 'SUCCESS' ? (
                        <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-success/15 text-sem-success border border-sem-success/30">
                          SUCCESS
                        </span>
                      ) : h.status === 'FAILED' ? (
                        <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-danger/15 text-sem-danger border border-sem-danger/30">
                          FAILED
                        </span>
                      ) : (
                        <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
                          DRAFT
                        </span>
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
