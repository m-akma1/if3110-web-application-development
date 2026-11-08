import React, { useState } from 'react'
import { MigrationView } from '../types'
import { api } from '../api/client'

interface BatchProgressMonitorProps {
  migration: MigrationView
  canEdit?: boolean
  onUpdated?: () => void
}

export const BatchProgressMonitor: React.FC<BatchProgressMonitorProps> = ({
  migration,
  canEdit = true,
  onUpdated,
}) => {
  const [actionLoading, setActionLoading] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [actionSuccess, setActionSuccess] = useState<string | null>(null)

  const committed = migration.batch_committed_count ?? 0
  const total = migration.batch_total_count && migration.batch_total_count > 0 ? migration.batch_total_count : 0
  const percent = total > 0 ? Math.min(100, Math.max(0, Math.round((committed / total) * 100))) : 0

  const isRunning = migration.status === 'RUNNING' || migration.status === 'QUEUED'
  const isPartial = migration.status === 'PARTIAL'
  const isSuccess = migration.status === 'SUCCESS'
  const isFailed = migration.status === 'FAILED'
  const isAborted = migration.status === 'ABORTED'

  const handleResume = async () => {
    const cursor = migration.checkpoint_cursor ?? committed
    const confirmed = window.confirm(
      `Lanjutkan eksekusi migrasi "${migration.name}" dari checkpoint cursor (${cursor})?`
    )
    if (!confirmed) return

    setActionLoading(true)
    setActionError(null)
    setActionSuccess(null)
    try {
      await api.resumeMigration(migration.id)
      setActionSuccess('Perintah resume berhasil dikirim. Melanjutkan eksekusi batch...')
      if (onUpdated) onUpdated()
    } catch (err: any) {
      setActionError(err.data?.error || err.message || 'Gagal melanjutkan migrasi dari checkpoint')
    } finally {
      setActionLoading(false)
    }
  }

  const handleAbort = async (withRollback: boolean) => {
    const message = withRollback
      ? `Konfirmasi Pembatalan: Apakah Anda yakin ingin membatalkan migrasi dan me-rollback ${committed} batch yang sudah tersimpan?`
      : `Konfirmasi Pembatalan: Apakah Anda ingin menandai migrasi sebagai dihentikan (tanpa rollback)?`
    const confirmed = window.confirm(message)
    if (!confirmed) return

    setActionLoading(true)
    setActionError(null)
    setActionSuccess(null)
    try {
      await api.abortPartialMigration(migration.id, { rollback: withRollback })
      setActionSuccess(
        withRollback
          ? 'Perintah pembatalan dan rollback berhasil dikirim.'
          : 'Migrasi berhasil ditandai sebagai dihentikan.'
      )
      if (onUpdated) onUpdated()
    } catch (err: any) {
      setActionError(err.data?.error || err.message || 'Gagal membatalkan migrasi')
    } finally {
      setActionLoading(false)
    }
  }

  return (
    <div className="bg-app-card border border-app-border rounded-xl p-5 space-y-4 shadow-lg">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2.5">
          <div className="w-2.5 h-2.5 rounded-full bg-brand-primary" />
          <h3 className="text-xs font-bold text-zinc-300 uppercase tracking-wider">
            Monitor Eksekusi Per-Batch
          </h3>
          {isRunning && (
            <span className="flex items-center gap-1.5 px-2 py-0.5 rounded text-[10px] font-bold bg-sem-info/15 text-sem-info border border-sem-info/30">
              <span className="w-1.5 h-1.5 rounded-full bg-sem-info animate-ping inline-block" />
              LIVE EXECUTION
            </span>
          )}
          {isPartial && (
            <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
              STATUS PARTIAL
            </span>
          )}
          {isSuccess && (
            <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-success/15 text-sem-success border border-sem-success/30">
              COMPLETED
            </span>
          )}
          {isFailed && (
            <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-danger/15 text-sem-danger border border-sem-danger/30">
              FAILED
            </span>
          )}
          {isAborted && (
            <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-zinc-500/15 text-zinc-400 border border-zinc-500/30">
              ABORTED
            </span>
          )}
        </div>

        <div className="text-xs font-mono text-zinc-400">
          Mode: <span className="text-zinc-200 font-semibold">{migration.execution_mode || 'BATCH'}</span>
        </div>
      </div>

      {/* Progress Bar Container */}
      <div className="space-y-2">
        <div className="flex justify-between items-center text-xs">
          <span className="text-zinc-400 font-medium">
            {isRunning
              ? 'Memproses data per-batch...'
              : isPartial
              ? 'Terhenti pada checkpoint'
              : isSuccess
              ? 'Seluruh batch selesai diproses'
              : 'Status eksekusi batch'}
          </span>
          <span className="font-mono font-bold text-zinc-200">
            {total > 0 ? `${percent}%` : committed > 0 ? `${committed} batch` : '0%'}
          </span>
        </div>

        <div className="w-full h-3.5 bg-app-input rounded-full overflow-hidden border border-app-border relative">
          <div
            className={`h-full transition-all duration-500 ease-out ${
              isPartial
                ? 'bg-gradient-to-r from-sem-warning to-amber-500'
                : isFailed
                ? 'bg-sem-danger'
                : isSuccess
                ? 'bg-sem-success'
                : 'bg-gradient-to-r from-brand-primary to-brand-primary-hover'
            }`}
            style={{ width: `${total > 0 ? percent : isRunning ? 50 : 0}%` }}
          />
        </div>

        <div className="flex justify-between items-center text-[11px] text-zinc-500 font-mono">
          <span>
            Tersimpan: <strong className="text-zinc-300">{committed}</strong> dari{' '}
            <strong className="text-zinc-300">{total > 0 ? total : '?'}</strong> batch
          </span>
          {migration.checkpoint_cursor !== undefined && migration.checkpoint_cursor !== null && (
            <span>
              Cursor Checkpoint: <strong className="text-brand-primary">{String(migration.checkpoint_cursor)}</strong>
            </span>
          )}
        </div>
      </div>

      {/* Current phase indicator */}
      {migration.current_phase && (
        <div className="p-2.5 rounded-lg bg-app-input border border-app-border flex items-center justify-between text-xs">
          <span className="text-zinc-400">Fase Saat Ini:</span>
          <span className="text-brand-primary font-mono font-medium">{migration.current_phase}</span>
        </div>
      )}

      {/* Metrics Grid */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3 pt-1 border-t border-app-border/60">
        <div className="bg-app-input/50 p-2.5 rounded-lg border border-app-border/40">
          <div className="text-[10px] uppercase font-bold text-zinc-500">Ukuran Batch</div>
          <div className="text-xs font-mono font-semibold text-zinc-200 mt-0.5">
            {migration.batch_size ? `${migration.batch_size} baris` : '1000 baris'}
          </div>
        </div>
        <div className="bg-app-input/50 p-2.5 rounded-lg border border-app-border/40">
          <div className="text-[10px] uppercase font-bold text-zinc-500">Kolom Kursor</div>
          <div className="text-xs font-mono font-semibold text-zinc-200 mt-0.5">
            {migration.batch_attribute || 'id'}
          </div>
        </div>
        <div className="bg-app-input/50 p-2.5 rounded-lg border border-app-border/40">
          <div className="text-[10px] uppercase font-bold text-zinc-500">Batch Berhasil</div>
          <div className="text-xs font-mono font-semibold text-sem-success mt-0.5">
            {committed} batch
          </div>
        </div>
        <div className="bg-app-input/50 p-2.5 rounded-lg border border-app-border/40">
          <div className="text-[10px] uppercase font-bold text-zinc-500">Sisa Batch</div>
          <div className="text-xs font-mono font-semibold text-zinc-300 mt-0.5">
            {total > 0 ? Math.max(0, total - committed) : '?'} batch
          </div>
        </div>
      </div>

      {/* Action feedback */}
      {actionSuccess && (
        <div className="p-3 rounded-lg text-xs font-mono bg-sem-success/10 border border-sem-success/30 text-sem-success">
          {actionSuccess}
        </div>
      )}
      {actionError && (
        <div className="p-3 rounded-lg text-xs font-mono bg-sem-danger/10 border border-sem-danger/30 text-sem-danger">
          {actionError}
        </div>
      )}

      {/* Special PARTIAL Status Warning & Recovery Section */}
      {isPartial && (
        <div className="p-4 rounded-xl bg-sem-warning/10 border border-sem-warning/30 space-y-3">
          <div className="flex items-start justify-between gap-3">
            <div>
              <h4 className="text-xs font-bold text-sem-warning flex items-center gap-1.5 uppercase tracking-wider">
                Eksekusi Terhenti Sebagian (Status PARTIAL)
              </h4>
              <p className="text-xs text-zinc-300 mt-1">
                Batch 1 sampai {committed} telah berhasil di-commit ke database. Eksekusi terhenti sebelum mencapai batch akhir.
              </p>
            </div>
          </div>

          {(migration.error || migration.batch_error) && (
            <div className="space-y-1">
              <span className="text-[10px] uppercase font-bold text-zinc-400">Penyebab Terhenti:</span>
              <pre className="p-2.5 rounded bg-app-bg border border-sem-warning/20 text-sem-danger font-mono text-[11px] whitespace-pre-wrap">
                {migration.error || migration.batch_error}
              </pre>
            </div>
          )}

          <div className="text-xs text-zinc-400">
            Anda dapat melanjutkan eksekusi dari checkpoint terakhir atau membatalkan batch yang telah tersimpan.
          </div>

          {canEdit && (
            <div className="flex flex-wrap items-center gap-2 pt-1">
              <button
                type="button"
                disabled={actionLoading}
                onClick={handleResume}
                className="px-4 py-2 rounded-lg text-xs font-bold bg-brand-primary text-app-bg hover:bg-brand-primary-hover transition cursor-pointer disabled:opacity-50 shadow-md"
              >
                {actionLoading ? 'Memproses...' : 'Lanjutkan dari Checkpoint'}
              </button>

              <button
                type="button"
                disabled={actionLoading}
                onClick={() => handleAbort(true)}
                className="px-3.5 py-2 rounded-lg text-xs font-bold bg-sem-danger/15 text-sem-danger hover:bg-sem-danger/25 border border-sem-danger/30 transition cursor-pointer disabled:opacity-50"
              >
                Batalkan & Rollback Batch
              </button>

              <button
                type="button"
                disabled={actionLoading}
                onClick={() => handleAbort(false)}
                className="px-3.5 py-2 rounded-lg text-xs font-semibold text-zinc-400 hover:text-zinc-200 hover:bg-app-active/40 border border-app-border transition cursor-pointer disabled:opacity-50"
              >
                Tandai Dihentikan
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
