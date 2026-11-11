import React, { useState, useEffect } from 'react'
import { useSearchParams, useNavigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { api } from '../api/client'
import { MigrationView, DryRunView } from '../types'
import { NoDatabaseAccess } from '../components/NoDatabaseAccess'
import { MigrationDraftSidebar } from '../components/migration/MigrationDraftSidebar'
import { MigrationEditor } from '../components/migration/MigrationEditor'
import { MigrationHistoryTable } from '../components/migration/MigrationHistoryTable'

export const MigrationPage: React.FC = () => {
  const { user, groups, activeDBID } = useAuth()
  const [searchParams, setSearchParams] = useSearchParams()
  const navigate = useNavigate()

  const draftParam = searchParams.get('draft') || ''

  const [, setLoading] = useState(true)
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
    setSqlTab(d.execution_type === 'ROLLBACK' ? 'down' : 'up')
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

  const handleRollback = async (m: MigrationView) => {
    if (m.is_irreversible) {
      alert(`Migrasi "${m.name}" tidak dapat di-rollback karena ditandai sebagai irreversible.\nAlasan: ${m.irreversible_reason || '-'}`)
      return
    }
    const ok = window.confirm(`Apakah Anda yakin ingin mengajukan/menjalankan ROLLBACK untuk migrasi "${m.name}"?`)
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      const res = await api.rollbackMigration(m.id)
      if (res.requires_approval) {
        setFeedback({ success: true, message: res.message || `Permintaan rollback untuk "${m.name}" telah diajukan untuk persetujuan Group Leader.` })
        await fetchOverview(activeDBID, res.migration?.id || draftParam)
      } else {
        setFeedback({ success: true, message: `Rollback untuk migrasi "${m.name}" berhasil dijalankan.` })
        await fetchOverview(activeDBID, draftParam)
      }
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menjalankan rollback' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleForward = async (m: MigrationView) => {
    const ok = window.confirm(`Apakah Anda yakin ingin mengajukan/menjalankan FORWARD (mengaplikasikan kembali) migrasi "${m.name}"?`)
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      const res = await api.forwardMigration(m.id)
      if (res.requires_approval) {
        setFeedback({ success: true, message: res.message || `Permintaan forward untuk "${m.name}" telah diajukan untuk persetujuan Group Leader.` })
        await fetchOverview(activeDBID, res.migration?.id || draftParam)
      } else {
        setFeedback({ success: true, message: `Forward untuk migrasi "${m.name}" berhasil dijalankan.` })
        await fetchOverview(activeDBID, draftParam, false)
      }
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

  // Filter history based on execution ID pairing (rollback_target_id)
  const hiddenRollbackIDs = new Set<string>()
  history.forEach((h) => {
    if (h.execution_type?.toUpperCase() === 'ROLLBACK' && h.status?.toUpperCase() === 'SUCCESS') {
      hiddenRollbackIDs.add(h.id)
      if (h.rollback_target_id) {
        hiddenRollbackIDs.add(h.rollback_target_id)
      }
    }
  })

  const filteredHistory = history.filter((h) => {
    if (!hideRollback) return true
    if (hiddenRollbackIDs.has(h.id)) return false
    return true
  })

  if (!activeDBID) {
    return <NoDatabaseAccess groups={groups} />
  }

  const isLocked = Boolean(
    selectedDraft &&
      (selectedDraft.status === 'APPROVED' ||
        selectedDraft.status === 'PENDING_REVIEW' ||
        selectedDraft.execution_type === 'ROLLBACK' ||
        Boolean(selectedDraft.source_migration_id))
  )

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
          <MigrationDraftSidebar
            drafts={drafts}
            selectedDraft={selectedDraft}
            onSelectDraft={handleSelectDraft}
          />

          <MigrationEditor
            selectedDraft={selectedDraft}
            user={user}
            groups={groups}
            name={name}
            setName={setName}
            sqlTab={sqlTab}
            setSqlTab={setSqlTab}
            sqlUp={sqlUp}
            setSqlUp={setSqlUp}
            sqlDown={sqlDown}
            setSqlDown={setSqlDown}
            isIrreversible={isIrreversible}
            setIsIrreversible={setIsIrreversible}
            irreversibleReason={irreversibleReason}
            setIrreversibleReason={setIrreversibleReason}
            executionMode={executionMode}
            setExecutionMode={setExecutionMode}
            batchAttribute={batchAttribute}
            setBatchAttribute={setBatchAttribute}
            batchSize={batchSize}
            setBatchSize={setBatchSize}
            isLocked={isLocked}
            actionLoading={actionLoading}
            feedback={feedback}
            dryRunReport={dryRunReport}
            onFileUpload={handleFileUpload}
            onSaveDraft={handleSaveDraft}
            onDeleteDraft={handleDeleteDraft}
            onDryRun={handleDryRun}
            onSubmitReview={handleSubmitReview}
            onExecute={handleExecute}
          />
        </div>
      ) : (
        <div className="p-4 rounded-xl bg-app-input border border-app-border text-xs text-zinc-400">
          Anda memiliki akses <span className="font-semibold text-zinc-200">View Only</span> pada database ini: riwayat migrasi dapat dilihat, tetapi membuat dan menjalankan migrasi membutuhkan akses View + Edit melalui group.
        </div>
      )}

      <MigrationHistoryTable
        history={history}
        filteredHistory={filteredHistory}
        hideRollback={hideRollback}
        setHideRollback={setHideRollback}
        canEdit={canEdit}
        actionLoading={actionLoading}
        onRollback={handleRollback}
        onForward={handleForward}
        onResumeBatch={handleResumeBatch}
        onAbortBatch={handleAbortBatch}
      />
    </div>
  )
}
