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

  // Editor form state
  const [selectedDraft, setSelectedDraft] = useState<MigrationView | null>(null)
  const [name, setName] = useState('')
  const [sqlTab, setSqlTab] = useState<'up' | 'down'>('up')
  const [sqlUp, setSqlUp] = useState('')
  const [sqlDown, setSqlDown] = useState('')
  const [isIrreversible, setIsIrreversible] = useState(false)
  const [irreversibleReason, setIrreversibleReason] = useState('')
  const [executionMode, setExecutionMode] = useState<'STANDARD' | 'BATCH'>('STANDARD')
  const [batchAttribute, setBatchAttribute] = useState('id')
  const [batchSize, setBatchSize] = useState<number>(1000)

  // Filter state
  const [hideRollback, setHideRollback] = useState(false)

  // Operation state
  const [feedback, setFeedback] = useState<{ success: boolean; message: string } | null>(null)
  const [dryRunReport, setDryRunReport] = useState<DryRunView | null>(null)
  const [actionLoading, setActionLoading] = useState(false)

  const resetForm = () => {
    setSelectedDraft(null)
    setName('')
    setSqlTab('up')
    setSqlUp('')
    setSqlDown('')
    setIsIrreversible(false)
    setIrreversibleReason('')
    setExecutionMode('STANDARD')
    setBatchAttribute('id')
    setBatchSize(1000)
  }

  const applyDraftToState = (d: MigrationView) => {
    setSelectedDraft(d)
    setName(d.name || '')
    setSqlUp(d.sql_up || d.sql || '')
    setSqlDown(d.sql_down || '')
    setIsIrreversible(!!d.is_irreversible)
    setIrreversibleReason(d.irreversible_reason || '')
    setExecutionMode((d.execution_mode as 'STANDARD' | 'BATCH') || 'STANDARD')
    setBatchAttribute(d.batch_attribute || 'id')
    setBatchSize(d.batch_size || 1000)
    setSqlTab('up')
  }

  const fetchOverview = async (dbId: string, currentDraftId?: string, showSpinner = true) => {
    if (showSpinner) setLoading(true)
    try {
      const res = await api.getMigrationOverview(dbId, currentDraftId)
      setCanEdit(res.can_edit)
      setDbName(res.database_name || '')
      setDrafts(res.drafts || [])
      setHistory(res.history || [])

      if (currentDraftId) {
        const found = (res.drafts || []).find((d: MigrationView) => d.id === currentDraftId)
        if (found) {
          applyDraftToState(found)
        } else {
          resetForm()
        }
      } else {
        resetForm()
      }
    } catch (err: any) {
      // handled
    } finally {
      if (showSpinner) setLoading(false)
    }
  }

  useEffect(() => {
    if (activeDBID) {
      fetchOverview(activeDBID, draftParam, true)
    } else {
      setLoading(false)
    }
    setFeedback(null)
    setDryRunReport(null)
  }, [activeDBID, draftParam])

  // Polling when any migration in history is RUNNING or QUEUED
  useEffect(() => {
    const hasRunning = history.some((h) => h.status === 'RUNNING' || h.status === 'QUEUED')
    if (!hasRunning || !activeDBID) return

    const interval = setInterval(() => {
      fetchOverview(activeDBID, draftParam, false)
    }, 2500)
    return () => clearInterval(interval)
  }, [history, activeDBID, draftParam])

  const handleSelectDraft = (d: MigrationView | null) => {
    setFeedback(null)
    setDryRunReport(null)
    if (d) {
      setSearchParams({ draft: d.id })
    } else {
      resetForm()
      setSearchParams({})
    }
  }

  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = (evt) => {
      const text = evt.target?.result as string
      if (sqlTab === 'up') {
        setSqlUp(text)
      } else {
        setSqlDown(text)
      }
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
    if (isIrreversible && !irreversibleReason.trim()) {
      setFeedback({ success: false, message: 'Alasan mengapa migrasi ini irreversible wajib diisi' })
      return
    }

    setActionLoading(true)
    setFeedback(null)
    try {
      const payload = {
        db_id: activeDBID,
        name: name.trim(),
        sql_script: sqlUp,
        sql_up: sqlUp,
        sql_down: isIrreversible ? '' : sqlDown,
        is_irreversible: isIrreversible,
        irreversible_reason: isIrreversible ? irreversibleReason.trim() : '',
        execution_mode: executionMode,
        batch_attribute: executionMode === 'BATCH' ? batchAttribute.trim() : undefined,
        batch_size: executionMode === 'BATCH' ? Number(batchSize) || 1000 : undefined,
      }

      if (!selectedDraft) {
        const res = await api.createMigrationDraft(payload)
        setFeedback({ success: true, message: 'Draft berhasil dibuat.' })
        await fetchOverview(activeDBID, res.draft.id)
        setSearchParams({ draft: res.draft.id })
      } else {
        const res = await api.saveMigrationDraft(selectedDraft.id, payload)
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
        sql_script: sqlUp,
        sql_up: sqlUp,
        sql_down: isIrreversible ? '' : sqlDown,
        is_irreversible: isIrreversible,
        irreversible_reason: isIrreversible ? irreversibleReason.trim() : '',
        execution_mode: executionMode,
        batch_attribute: executionMode === 'BATCH' ? batchAttribute.trim() : undefined,
        batch_size: executionMode === 'BATCH' ? Number(batchSize) || 1000 : undefined,
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
    if (isIrreversible && !irreversibleReason.trim()) {
      setFeedback({ success: false, message: 'Alasan mengapa migrasi ini irreversible wajib diisi sebelum eksekusi' })
      return
    }

    const promptMsg = isIrreversible
      ? 'PERINGATAN: Migrasi ini ditandai sebagai IRREVERSIBLE (tidak dapat di-rollback).\n\nApakah Anda yakin ingin mengeksekusinya pada database target?'
      : 'Simpan dan eksekusi migration ini pada database target? Hasilnya (COMMIT atau ROLLBACK) dicatat permanen di riwayat.'

    const ok = window.confirm(promptMsg)
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      const res = await api.executeMigration(selectedDraft.id, {
        name: name.trim(),
        sql_script: sqlUp,
        sql_up: sqlUp,
        sql_down: isIrreversible ? '' : sqlDown,
        is_irreversible: isIrreversible,
        irreversible_reason: isIrreversible ? irreversibleReason.trim() : '',
        execution_mode: executionMode,
        batch_attribute: executionMode === 'BATCH' ? batchAttribute.trim() : undefined,
        batch_size: executionMode === 'BATCH' ? Number(batchSize) || 1000 : undefined,
      })
      navigate(`/migration/${res.migration.id}`)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Eksekusi migration gagal' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleSubmitReview = async () => {
    if (!selectedDraft) return
    const ok = window.confirm(`Ajukan migrasi "${name || selectedDraft.name}" untuk ditinjau oleh Group Leader?`)
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.submitMigrationForReview(selectedDraft.id)
      setFeedback({ success: true, message: 'Migrasi berhasil diajukan untuk review Group Leader.' })
      await fetchOverview(activeDBID, selectedDraft.id)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal mengajukan review' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleApprove = async () => {
    if (!selectedDraft) return
    const note = window.prompt('Tambahkan catatan persetujuan (opsional):', '')
    if (note === null) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.approveMigration(selectedDraft.id, note)
      setFeedback({ success: true, message: 'Migrasi telah disetujui (Approved) dan siap dieksekusi.' })
      await fetchOverview(activeDBID, selectedDraft.id)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menyetujui migrasi' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleReject = async () => {
    if (!selectedDraft) return
    const reason = window.prompt('Masukkan alasan penolakan migrasi (wajib):', '')
    if (!reason || !reason.trim()) {
      alert('Alasan penolakan wajib diisi.')
      return
    }

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.rejectMigration(selectedDraft.id, reason.trim())
      setFeedback({ success: true, message: 'Migrasi telah ditolak.' })
      await fetchOverview(activeDBID, selectedDraft.id)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menolak migrasi' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleRollback = async (m: MigrationView) => {
    if (m.is_irreversible) {
      alert(`Migrasi "${m.name}" tidak dapat di-rollback karena ditandai sebagai irreversible.\nAlasan: ${m.irreversible_reason || '-'}`)
      return
    }
    const ok = window.confirm(`Apakah Anda yakin ingin melakukan ROLLBACK untuk migrasi "${m.name}"?`)
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.rollbackMigration(m.id)
      setFeedback({ success: true, message: `Rollback untuk migrasi "${m.name}" berhasil dijalankan.` })
      await fetchOverview(activeDBID, draftParam)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menjalankan rollback' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleForward = async (m: MigrationView) => {
    const ok = window.confirm(`Apakah Anda yakin ingin melakukan FORWARD (mengaplikasikan kembali) migrasi "${m.name}"?`)
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.forwardMigration(m.id)
      setFeedback({ success: true, message: `Forward untuk migrasi "${m.name}" berhasil dijalankan.` })
      await fetchOverview(activeDBID, draftParam, false)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menjalankan forward' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleResumeBatch = async (m: MigrationView) => {
    const cursor = m.checkpoint_cursor ?? m.batch_committed_count ?? 0
    const ok = window.confirm(`Lanjutkan eksekusi migrasi "${m.name}" dari checkpoint cursor (${cursor})?`)
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.resumeMigration(m.id)
      setFeedback({ success: true, message: `Eksekusi migrasi "${m.name}" berhasil dilanjutkan.` })
      await fetchOverview(activeDBID, draftParam, false)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal melanjutkan migrasi dari checkpoint' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleAbortBatch = async (m: MigrationView) => {
    const ok = window.confirm(
      `Konfirmasi Pembatalan: Apakah Anda yakin ingin membatalkan migrasi "${m.name}" dan me-rollback ${m.batch_committed_count || 0} batch yang sudah tersimpan?`
    )
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.abortPartialMigration(m.id, { rollback: true })
      setFeedback({ success: true, message: `Migrasi "${m.name}" berhasil dibatalkan dan di-rollback.` })
      await fetchOverview(activeDBID, draftParam, false)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal membatalkan migrasi' })
    } finally {
      setActionLoading(false)
    }
  }

  // Filter history
  const filteredHistory = history.filter((h) => {
    if (!hideRollback) return true
    if (h.execution_type?.toUpperCase() === 'ROLLBACK') return false
    if (h.status?.toUpperCase() === 'ROLLED_BACK') return false
    if (h.name.toLowerCase().startsWith('rollback:')) return false
    return true
  })

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
            Mendukung skema perubahan ganda (SQL Up & Down), penandaan irreversible, dan mode eksekusi bertahap (batching).
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
                    <div className="flex items-center justify-between gap-1">
                      <span className="font-semibold truncate">{d.name}</span>
                      {d.status === 'PENDING_REVIEW' && (
                        <span className="text-[9px] px-1 py-0.2 rounded bg-sem-warning/20 text-sem-warning font-semibold shrink-0">
                          Review
                        </span>
                      )}
                      {d.status === 'APPROVED' && (
                        <span className="text-[9px] px-1 py-0.2 rounded bg-sem-success/20 text-sem-success font-semibold shrink-0">
                          Approved
                        </span>
                      )}
                      {d.status === 'REJECTED' && (
                        <span className="text-[9px] px-1 py-0.2 rounded bg-sem-danger/20 text-sem-danger font-semibold shrink-0">
                          Rejected
                        </span>
                      )}
                    </div>
                    <div className="text-[10px] text-zinc-500 mt-0.5">
                      {d.created_by_name || '-'} · {fmtTime(d.updated_at)}
                    </div>
                  </button>
                )
              })
            )}
          </div>

          {/* Migration Editor Form */}
          <div id="migration-editor" className="bg-app-card border border-app-border rounded-xl p-5 space-y-4 shadow-xl">
            <form onSubmit={(e) => e.preventDefault()} className="space-y-4">
              {/* Migration Name & File Upload */}
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

              {/* Dual SQL Tabs */}
              <div className="space-y-0">
                <div className="flex items-center justify-between border-b border-app-border">
                  <div className="flex space-x-1">
                    <button
                      type="button"
                      onClick={() => setSqlTab('up')}
                      className={`px-4 py-2 text-xs font-semibold rounded-t-lg transition flex items-center gap-1.5 cursor-pointer ${
                        sqlTab === 'up'
                          ? 'bg-app-bg text-brand-primary border-t-2 border-brand-primary border-x border-app-border'
                          : 'text-zinc-400 hover:text-zinc-200 hover:bg-app-active/40'
                      }`}
                    >
                      <span>SQL Up (Apply)</span>
                      <span className="text-[10px] px-1.5 py-0.2 rounded bg-sem-info/10 text-sem-info font-mono">
                        {sqlUp.trim() ? `${sqlUp.trim().split('\n').length} baris` : 'kosong'}
                      </span>
                    </button>
                    <button
                      type="button"
                      onClick={() => setSqlTab('down')}
                      className={`px-4 py-2 text-xs font-semibold rounded-t-lg transition flex items-center gap-1.5 cursor-pointer ${
                        sqlTab === 'down'
                          ? 'bg-app-bg text-brand-primary border-t-2 border-brand-primary border-x border-app-border'
                          : 'text-zinc-400 hover:text-zinc-200 hover:bg-app-active/40'
                      }`}
                    >
                      <span>SQL Down (Rollback)</span>
                      {isIrreversible ? (
                        <span className="text-[10px] px-1.5 py-0.2 rounded bg-sem-warning/15 text-sem-warning font-semibold">
                          Irreversible
                        </span>
                      ) : (
                        <span className="text-[10px] px-1.5 py-0.2 rounded bg-sem-info/10 text-sem-info font-mono">
                          {sqlDown.trim() ? `${sqlDown.trim().split('\n').length} baris` : 'kosong'}
                        </span>
                      )}
                    </button>
                  </div>
                  <div className="text-[11px] text-zinc-400 hidden sm:block px-2">
                    {sqlTab === 'up'
                      ? 'Perubahan skema yang diaplikasikan'
                      : isIrreversible
                      ? 'Rollback dinonaktifkan (Irreversible)'
                      : 'Perintah pemulihan skema jika terjadi rollback'}
                  </div>
                </div>

                {sqlTab === 'up' ? (
                  <textarea
                    id="sql_script_up"
                    name="sql_script_up"
                    rows={12}
                    spellCheck="false"
                    value={sqlUp}
                    onChange={(e) => setSqlUp(e.target.value)}
                    placeholder="-- Tulis perintah SQL perubahan skema di sini (SQL Up). Misal: CREATE TABLE users (...); ALTER TABLE orders ADD COLUMN..."
                    className="w-full p-4 bg-app-bg border border-app-border rounded-b-lg font-mono text-xs text-sem-info focus:outline-none focus:ring-2 focus:ring-brand-primary/50 leading-relaxed resize-y"
                  />
                ) : isIrreversible ? (
                  <div className="p-6 bg-app-bg border border-app-border rounded-b-lg space-y-3">
                    <div className="flex items-start gap-3 p-3.5 rounded-lg bg-sem-warning/10 border border-sem-warning/30 text-sem-warning">
                      <span className="text-base">⚠️</span>
                      <div className="space-y-1 text-xs">
                        <div className="font-bold">Migrasi ini ditandai sebagai Irreversible</div>
                        <p className="text-zinc-300">
                          Script SQL Down dinonaktifkan karena migrasi ini bersifat permanen dan tidak dapat dibatalkan secara otomatis.
                        </p>
                        {irreversibleReason.trim() && (
                          <div className="mt-2 text-[11px] font-mono bg-app-input p-2 rounded border border-sem-warning/20 text-zinc-200">
                            <span className="text-sem-warning font-semibold">Alasan: </span>
                            {irreversibleReason}
                          </div>
                        )}
                      </div>
                    </div>
                  </div>
                ) : (
                  <textarea
                    id="sql_script_down"
                    name="sql_script_down"
                    rows={12}
                    spellCheck="false"
                    value={sqlDown}
                    onChange={(e) => setSqlDown(e.target.value)}
                    placeholder="-- Tulis perintah SQL untuk mengembalikan perubahan (SQL Down). Misal: DROP TABLE users; ALTER TABLE orders DROP COLUMN..."
                    className="w-full p-4 bg-app-bg border border-app-border rounded-b-lg font-mono text-xs text-sem-info focus:outline-none focus:ring-2 focus:ring-brand-primary/50 leading-relaxed resize-y"
                  />
                )}
              </div>

              {/* M2 Options: Irreversible & Execution Mode */}
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4 pt-1">
                {/* Option: Irreversible */}
                <div className="p-4 rounded-xl bg-app-bg border border-app-border space-y-3">
                  <div className="flex items-center justify-between">
                    <label className="flex items-center gap-2.5 cursor-pointer">
                      <input
                        type="checkbox"
                        checked={isIrreversible}
                        onChange={(e) => setIsIrreversible(e.target.checked)}
                        className="w-4 h-4 rounded border-app-border bg-app-card text-brand-primary focus:ring-brand-primary accent-[#ceafa3]"
                      />
                      <span className="text-xs font-semibold text-zinc-200">Tandai sebagai Irreversible</span>
                    </label>
                    {isIrreversible && (
                      <span className="text-[10px] font-bold px-2 py-0.5 rounded bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
                        No Rollback
                      </span>
                    )}
                  </div>
                  <p className="text-[11px] text-zinc-400 leading-normal">
                    Gunakan jika migrasi ini bersifat destruktif atau satu arah sehingga tidak dapat di-rollback.
                  </p>

                  {isIrreversible && (
                    <div className="space-y-1.5 pt-1">
                      <label className="block text-[11px] font-medium text-zinc-300">
                        Alasan Irreversible <span className="text-sem-danger">*</span>
                      </label>
                      <textarea
                        rows={2}
                        value={irreversibleReason}
                        onChange={(e) => setIrreversibleReason(e.target.value)}
                        placeholder="Jelaskan mengapa migrasi ini tidak dapat dibatalkan..."
                        className="w-full px-3 py-2 bg-app-input border border-app-border rounded-lg text-xs text-zinc-100 placeholder:text-zinc-600 focus:outline-none focus:ring-2 focus:ring-brand-primary/50 resize-none"
                      />
                    </div>
                  )}
                </div>

                {/* Option: Mode Eksekusi */}
                <div className="p-4 rounded-xl bg-app-bg border border-app-border space-y-3">
                  <div className="flex items-center justify-between">
                    <span className="text-xs font-semibold text-zinc-200">Mode Eksekusi</span>
                    <span className="text-[10px] font-mono text-zinc-400 px-2 py-0.5 rounded bg-app-input border border-app-border">
                      {executionMode === 'BATCH' ? 'Per-Batch' : 'Langsung'}
                    </span>
                  </div>

                  <div className="grid grid-cols-2 gap-2">
                    <button
                      type="button"
                      onClick={() => setExecutionMode('STANDARD')}
                      className={`px-3 py-2 rounded-lg text-xs font-medium border text-left transition cursor-pointer ${
                        executionMode === 'STANDARD'
                          ? 'bg-app-active border-brand-primary/60 text-zinc-100'
                          : 'bg-app-input border-app-border text-zinc-400 hover:text-zinc-200 hover:bg-app-card'
                      }`}
                    >
                      <div className="font-semibold text-xs">Langsung (Standard)</div>
                      <div className="text-[10px] text-zinc-500 mt-0.5">Satu transaksi ACID</div>
                    </button>

                    <button
                      type="button"
                      onClick={() => setExecutionMode('BATCH')}
                      className={`px-3 py-2 rounded-lg text-xs font-medium border text-left transition cursor-pointer ${
                        executionMode === 'BATCH'
                          ? 'bg-app-active border-brand-primary/60 text-zinc-100'
                          : 'bg-app-input border-app-border text-zinc-400 hover:text-zinc-200 hover:bg-app-card'
                      }`}
                    >
                      <div className="font-semibold text-xs">Per-batch (Batching)</div>
                      <div className="text-[10px] text-zinc-500 mt-0.5">Potongan data bertahap</div>
                    </button>
                  </div>

                  {executionMode === 'BATCH' && (
                    <div className="grid grid-cols-2 gap-2 pt-1">
                      <div>
                        <label className="block text-[10px] font-medium text-zinc-400 mb-1">
                          Ukuran Batch (Baris)
                        </label>
                        <input
                          type="number"
                          min={1}
                          max={100000}
                          value={batchSize}
                          onChange={(e) => setBatchSize(Number(e.target.value))}
                          className="w-full px-2.5 py-1.5 bg-app-input border border-app-border rounded-lg text-xs text-zinc-100 focus:outline-none focus:ring-2 focus:ring-brand-primary/50"
                        />
                      </div>
                      <div>
                        <label className="block text-[10px] font-medium text-zinc-400 mb-1">
                          Kolom Cursor / PK
                        </label>
                        <input
                          type="text"
                          value={batchAttribute}
                          onChange={(e) => setBatchAttribute(e.target.value)}
                          placeholder="id"
                          className="w-full px-2.5 py-1.5 bg-app-input border border-app-border rounded-lg text-xs text-zinc-100 focus:outline-none focus:ring-2 focus:ring-brand-primary/50"
                        />
                      </div>
                    </div>
                  )}
                </div>
              </div>

              {/* Workflow Review Banners */}
              {selectedDraft && selectedDraft.status === 'PENDING_REVIEW' && (
                <div className="p-3.5 rounded-xl bg-sem-warning/10 border border-sem-warning/30 text-xs text-sem-warning flex flex-wrap items-center justify-between gap-3">
                  <div>
                    <div className="font-bold flex items-center gap-1.5">
                      <span>⏳</span> Menunggu Review Group Leader
                    </div>
                    <p className="text-zinc-300 text-[11px] mt-0.5">
                      Migrasi ini diajukan untuk peninjauan dan memerlukan persetujuan sebelum dieksekusi.
                    </p>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <button
                      type="button"
                      disabled={actionLoading}
                      onClick={handleApprove}
                      className="px-3 py-1.5 rounded-lg text-xs font-bold bg-sem-success text-app-bg hover:opacity-90 transition cursor-pointer disabled:opacity-50"
                    >
                      Setujui
                    </button>
                    <button
                      type="button"
                      disabled={actionLoading}
                      onClick={handleReject}
                      className="px-3 py-1.5 rounded-lg text-xs font-bold bg-sem-danger/15 text-sem-danger hover:bg-sem-danger/25 border border-sem-danger/30 transition cursor-pointer disabled:opacity-50"
                    >
                      Tolak
                    </button>
                  </div>
                </div>
              )}

              {selectedDraft && selectedDraft.status === 'APPROVED' && (
                <div className="p-3 rounded-xl bg-sem-success/10 border border-sem-success/30 text-xs text-sem-success flex items-center gap-2">
                  <div>
                    <span className="font-bold">Migrasi Telah Disetujui (Approved)</span>
                    <span className="text-zinc-300 ml-1.5 text-[11px]">Siap dieksekusi ke database target.</span>
                  </div>
                </div>
              )}

              {selectedDraft && selectedDraft.status === 'REJECTED' && (
                <div className="p-3.5 rounded-xl bg-sem-danger/10 border border-sem-danger/30 text-xs text-sem-danger space-y-1">
                  <div className="font-bold flex items-center gap-1.5">
                    Pengajuan Migrasi Ditolak
                  </div>
                  {selectedDraft.rejection_reason && (
                    <p className="text-zinc-300 text-[11px]">
                      Alasan: <span className="font-mono text-sem-danger font-semibold">{selectedDraft.rejection_reason}</span>
                    </p>
                  )}
                  <p className="text-zinc-400 text-[10px]">
                    Anda dapat merevisi script SQL di atas lalu menyimpannya atau mengajukan review ulang.
                  </p>
                </div>
              )}

              {/* Action Buttons */}
              <div className="flex flex-wrap items-center justify-between gap-2 pt-2">
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
                      className="px-3 py-1.5 rounded-lg text-xs font-medium text-sem-danger hover:bg-sem-danger/10 transition cursor-pointer disabled:opacity-50"
                    >
                      Hapus Draft
                    </button>
                    <div className="flex flex-wrap gap-2">
                      <button
                        type="button"
                        disabled={actionLoading}
                        onClick={handleSaveDraft}
                        className="px-3 py-1.5 rounded-lg text-xs font-medium bg-app-active hover:bg-app-border-strong text-white transition cursor-pointer disabled:opacity-50"
                      >
                        Simpan
                      </button>
                      <button
                        type="button"
                        disabled={actionLoading}
                        onClick={handleDryRun}
                        className="px-3 py-1.5 rounded-lg text-xs font-medium bg-app-active hover:bg-app-border-strong text-white transition cursor-pointer disabled:opacity-50"
                      >
                        Dry Run + EXPLAIN
                      </button>
                      {(!selectedDraft.status || selectedDraft.status === 'DRAFT' || selectedDraft.status === 'REJECTED') && (
                        <button
                          type="button"
                          disabled={actionLoading}
                          onClick={handleSubmitReview}
                          className="px-3 py-1.5 rounded-lg text-xs font-semibold bg-sem-info/15 hover:bg-sem-info/25 text-sem-info border border-sem-info/30 transition cursor-pointer disabled:opacity-50"
                          title="Ajukan migrasi ini untuk direview oleh Group Leader"
                        >
                          Ajukan Review
                        </button>
                      )}
                      <button
                        type="button"
                        disabled={actionLoading}
                        onClick={handleExecute}
                        className="px-4 py-1.5 rounded-lg text-xs font-bold bg-brand-primary hover:bg-brand-primary-hover text-app-bg transition shadow-md cursor-pointer disabled:opacity-50"
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
                {feedback.message}
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

      {/* Migration History Section */}
      <div className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3 px-1">
          <div className="flex items-center gap-3">
            <h3 className="text-xs font-bold text-zinc-400 uppercase tracking-wider">
              Riwayat Eksekusi Migrasi
            </h3>
            <span className="text-[11px] text-zinc-500 font-mono">
              ({filteredHistory.length} dari {history.length})
            </span>
          </div>

          <label className="flex items-center gap-2 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={hideRollback}
              onChange={(e) => setHideRollback(e.target.checked)}
              className="w-3.5 h-3.5 rounded border-app-border bg-app-card text-brand-primary focus:ring-brand-primary accent-[#ceafa3]"
            />
            <span className="text-xs text-zinc-300 font-medium">Sembunyikan Rollback</span>
          </label>
        </div>

        <div className="bg-app-card border border-app-border rounded-xl overflow-x-auto shadow-xl">
          <table className="w-full text-left text-sm text-zinc-300">
            <thead className="bg-app-bg text-xs uppercase tracking-wider text-zinc-400 border-b border-app-border">
              <tr>
                <th className="px-4 py-3 font-semibold">Versi</th>
                <th className="px-4 py-3 font-semibold">Nama</th>
                <th className="px-4 py-3 font-semibold">Tipe / Mode</th>
                <th className="px-4 py-3 font-semibold">Waktu Eksekusi</th>
                <th className="px-4 py-3 font-semibold">Oleh</th>
                <th className="px-4 py-3 font-semibold">Durasi</th>
                <th className="px-4 py-3 font-semibold">Status</th>
                <th className="px-4 py-3 font-semibold">Aksi</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-app-border">
              {filteredHistory.length === 0 ? (
                <tr>
                  <td colSpan={8} className="px-4 py-8 text-center text-zinc-500 text-xs">
                    {history.length > 0 && hideRollback
                      ? 'Semua riwayat tersembunyi karena filter "Sembunyikan Rollback" aktif.'
                      : 'Belum ada riwayat migrasi pada database ini.'}
                  </td>
                </tr>
              ) : (
                filteredHistory.map((h) => {
                  const isRolledBack = h.status === 'ROLLED_BACK' || h.execution_type === 'ROLLBACK'
                  return (
                    <tr key={h.id} className="hover:bg-app-active/50 transition">
                      <td className="px-4 py-3 font-mono text-xs">{versionLabel(h.version)}</td>
                      <td className="px-4 py-3 text-xs font-semibold">
                        <Link to={`/migration/${h.id}`} className="text-zinc-200 hover:text-brand-primary hover:underline">
                          {h.name}
                        </Link>
                        {h.is_irreversible && (
                          <div className="text-[10px] text-sem-warning font-normal mt-0.5">
                            Irreversible: {h.irreversible_reason || 'Tidak ada rollback'}
                          </div>
                        )}
                      </td>
                      <td className="px-4 py-3 text-xs">
                        <div className="flex flex-wrap items-center gap-1.5">
                          {h.execution_type === 'ROLLBACK' ? (
                            <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-sem-danger/15 text-sem-danger border border-sem-danger/30">
                              ROLLBACK
                            </span>
                          ) : h.execution_type === 'FORWARD' ? (
                            <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-sem-info/15 text-sem-info border border-sem-info/30">
                              FORWARD
                            </span>
                          ) : (
                            <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-sem-info/10 text-sem-info border border-sem-info/20">
                              UP
                            </span>
                          )}

                          {h.execution_mode === 'BATCH' && (
                            <span className="px-1.5 py-0.5 rounded text-[10px] font-mono bg-app-input text-zinc-300 border border-app-border">
                              BATCH ({h.batch_size || 1000})
                            </span>
                          )}

                          {h.is_irreversible && (
                            <span className="px-1.5 py-0.5 rounded text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
                              IRREVERSIBLE
                            </span>
                          )}
                        </div>
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
                        ) : h.status === 'ROLLED_BACK' ? (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-zinc-500/15 text-zinc-400 border border-zinc-500/30">
                            ROLLED_BACK
                          </span>
                        ) : h.status === 'PENDING_REVIEW' ? (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
                            PENDING REVIEW
                          </span>
                        ) : h.status === 'APPROVED' ? (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-success/15 text-sem-success border border-sem-success/30">
                            APPROVED
                          </span>
                        ) : h.status === 'REJECTED' ? (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-danger/15 text-sem-danger border border-sem-danger/30">
                            REJECTED
                          </span>
                        ) : h.status === 'RUNNING' ? (
                          <div className="flex flex-col gap-0.5">
                            <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-info/15 text-sem-info border border-sem-info/30 animate-pulse text-center">
                              RUNNING
                            </span>
                            {h.batch_total_count && h.batch_total_count > 0 ? (
                              <span className="text-[10px] font-mono text-zinc-400">
                                {Math.round(((h.batch_committed_count || 0) / h.batch_total_count) * 100)}% ({h.batch_committed_count || 0}/{h.batch_total_count})
                              </span>
                            ) : null}
                          </div>
                        ) : h.status === 'PARTIAL' ? (
                          <div className="flex flex-col gap-0.5">
                            <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30 text-center">
                              PARTIAL
                            </span>
                            <span className="text-[10px] font-mono text-sem-warning">
                              Batch {h.batch_committed_count || 0}/{h.batch_total_count || '?'}
                            </span>
                          </div>
                        ) : h.status === 'ABORTED' ? (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-zinc-500/15 text-zinc-400 border border-zinc-500/30">
                            ABORTED
                          </span>
                        ) : (
                          <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
                            {h.status}
                          </span>
                        )}
                      </td>
                      <td className="px-4 py-3 text-xs whitespace-nowrap">
                        {canEdit && (
                          <div className="flex items-center gap-1.5">
                            {h.status === 'PARTIAL' && (
                              <>
                                <button
                                  type="button"
                                  disabled={actionLoading}
                                  onClick={() => handleResumeBatch(h)}
                                  className="px-2.5 py-1 rounded text-[11px] font-semibold text-app-bg bg-brand-primary hover:bg-brand-primary-hover transition cursor-pointer disabled:opacity-50"
                                  title="Lanjutkan eksekusi dari checkpoint terakhir"
                                >
                                  Lanjutkan
                                </button>
                                <button
                                  type="button"
                                  disabled={actionLoading}
                                  onClick={() => handleAbortBatch(h)}
                                  className="px-2.5 py-1 rounded text-[11px] font-semibold text-sem-danger hover:bg-sem-danger/10 border border-sem-danger/30 transition cursor-pointer disabled:opacity-50"
                                  title="Batalkan migrasi dan rollback batch yang sudah tersimpan"
                                >
                                  Batalkan
                                </button>
                              </>
                            )}
                            {h.status === 'SUCCESS' && !h.is_irreversible && h.execution_type !== 'ROLLBACK' && (
                              <button
                                type="button"
                                disabled={actionLoading}
                                onClick={() => handleRollback(h)}
                                className="px-2.5 py-1 rounded text-[11px] font-semibold text-sem-danger hover:bg-sem-danger/10 border border-sem-danger/30 transition cursor-pointer disabled:opacity-50"
                                title="Rollback migrasi ini (menjalankan SQL Down)"
                              >
                                Rollback
                              </button>
                            )}
                            {isRolledBack && (
                              <button
                                type="button"
                                disabled={actionLoading}
                                onClick={() => handleForward(h)}
                                className="px-2.5 py-1 rounded text-[11px] font-semibold text-sem-success hover:bg-sem-success/10 border border-sem-success/30 transition cursor-pointer disabled:opacity-50"
                                title="Forward migrasi ini (menjalankan kembali SQL Up)"
                              >
                                Forward
                              </button>
                            )}
                            {h.status === 'SUCCESS' && h.is_irreversible && (
                              <span className="text-[10px] text-zinc-500 italic">No rollback</span>
                            )}
                          </div>
                        )}
                      </td>
                    </tr>
                  )
                })
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
