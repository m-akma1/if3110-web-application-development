import React, { useState, useEffect } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { MigrationView } from '../types'

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

export const MigrationDetailPage: React.FC = () => {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()

  const [loading, setLoading] = useState(true)
  const [migration, setMigration] = useState<MigrationView | null>(null)
  const [canEdit, setCanEdit] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<{ success: boolean; message: string } | null>(null)
  const [actionLoading, setActionLoading] = useState(false)
  const [sqlTab, setSqlTab] = useState<'up' | 'down'>('up')

  const fetchDetail = () => {
    if (!id) return
    setLoading(true)
    api
      .getMigrationDetail(id)
      .then((res) => {
        setMigration(res.migration)
        setCanEdit(res.can_edit)
      })
      .catch((err) => {
        setError(err.message || 'Gagal memuat detail migration')
      })
      .finally(() => {
        setLoading(false)
      })
  }

  useEffect(() => {
    fetchDetail()
  }, [id])

  const handleDuplicate = async () => {
    if (!id) return
    setActionLoading(true)
    setFeedback(null)
    try {
      const res = await api.duplicateMigration(id)
      navigate(`/migration?draft=${res.draft.id}`)
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menduplikat migration' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleRollback = async () => {
    if (!migration) return
    if (migration.is_irreversible) {
      alert(`Migrasi tidak dapat di-rollback karena ditandai sebagai irreversible.\nAlasan: ${migration.irreversible_reason || '-'}`)
      return
    }
    const ok = window.confirm(`Apakah Anda yakin ingin melakukan ROLLBACK untuk migrasi "${migration.name}"?`)
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.rollbackMigration(migration.id)
      setFeedback({ success: true, message: `Rollback untuk "${migration.name}" berhasil dijalankan.` })
      fetchDetail()
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menjalankan rollback' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleForward = async () => {
    if (!migration) return
    const ok = window.confirm(`Apakah Anda yakin ingin melakukan FORWARD (mengaplikasikan kembali) migrasi "${migration.name}"?`)
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.forwardMigration(migration.id)
      setFeedback({ success: true, message: `Forward untuk "${migration.name}" berhasil dijalankan.` })
      fetchDetail()
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menjalankan forward' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleSubmitReview = async () => {
    if (!id) return
    const ok = window.confirm(`Ajukan migrasi "${migration?.name}" untuk ditinjau oleh Group Leader?`)
    if (!ok) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.submitMigrationForReview(id)
      setFeedback({ success: true, message: 'Migrasi berhasil diajukan untuk review Group Leader.' })
      fetchDetail()
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal mengajukan review' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleApprove = async () => {
    if (!id) return
    const note = window.prompt('Tambahkan catatan persetujuan (opsional):', '')
    if (note === null) return

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.approveMigration(id, note)
      setFeedback({ success: true, message: 'Migrasi telah disetujui (Approved) dan siap dieksekusi.' })
      fetchDetail()
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menyetujui migrasi' })
    } finally {
      setActionLoading(false)
    }
  }

  const handleReject = async () => {
    if (!id) return
    const reason = window.prompt('Masukkan alasan penolakan migrasi (wajib):', '')
    if (!reason || !reason.trim()) {
      alert('Alasan penolakan wajib diisi.')
      return
    }

    setActionLoading(true)
    setFeedback(null)
    try {
      await api.rejectMigration(id, reason.trim())
      setFeedback({ success: true, message: 'Migrasi telah ditolak.' })
      fetchDetail()
    } catch (err: any) {
      setFeedback({ success: false, message: err.data?.error || err.message || 'Gagal menolak migrasi' })
    } finally {
      setActionLoading(false)
    }
  }

  if (loading) {
    return <div className="text-xs text-zinc-400">Memuat detail migration...</div>
  }

  if (error || !migration) {
    return (
      <div className="space-y-4">
        <Link to="/migration" className="text-xs font-semibold text-brand-primary hover:underline">
          ← Kembali ke Migration Editor
        </Link>
        <div className="p-4 rounded-xl bg-sem-danger/10 border border-sem-danger/30 text-sem-danger text-xs">
          {error || 'Migration tidak ditemukan'}
        </div>
      </div>
    )
  }

  const isRolledBack = migration.status === 'ROLLED_BACK' || migration.execution_type === 'ROLLBACK'

  return (
    <div className="space-y-5 max-w-5xl">
      <Link to="/migration" className="text-xs font-semibold text-brand-primary hover:underline">
        ← Kembali ke Migration Editor
      </Link>

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="text-2xl font-bold text-zinc-100">{migration.name}</h1>
            {migration.status === 'SUCCESS' ? (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-success/15 text-sem-success border border-sem-success/30">
                SUCCESS
              </span>
            ) : migration.status === 'FAILED' ? (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-danger/15 text-sem-danger border border-sem-danger/30">
                FAILED
              </span>
            ) : migration.status === 'ROLLED_BACK' ? (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-zinc-500/15 text-zinc-400 border border-zinc-500/30">
                ROLLED_BACK
              </span>
            ) : migration.status === 'PENDING_REVIEW' ? (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
                PENDING REVIEW
              </span>
            ) : migration.status === 'APPROVED' ? (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-success/15 text-sem-success border border-sem-success/30">
                APPROVED
              </span>
            ) : migration.status === 'REJECTED' ? (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-danger/15 text-sem-danger border border-sem-danger/30">
                REJECTED
              </span>
            ) : (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
                {migration.status}
              </span>
            )}

            {migration.execution_type === 'ROLLBACK' ? (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-danger/15 text-sem-danger border border-sem-danger/30">
                ROLLBACK RUN
              </span>
            ) : migration.execution_type === 'FORWARD' ? (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-info/15 text-sem-info border border-sem-info/30">
                FORWARD RUN
              </span>
            ) : null}

            {migration.is_irreversible && (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
                IRREVERSIBLE
              </span>
            )}
          </div>
          <p className="text-zinc-400 mt-1 text-xs font-mono">{migration.id}</p>
        </div>

        {canEdit && (
          <div className="flex flex-wrap items-center gap-2">
            {migration.status === 'PENDING_REVIEW' && (
              <>
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
              </>
            )}

            {(migration.status === 'DRAFT' || migration.status === 'REJECTED') && (
              <>
                <Link
                  to={`/migration?draft=${migration.id}`}
                  className="px-4 py-1.5 rounded-lg text-xs font-bold bg-brand-primary hover:bg-brand-primary-hover text-app-bg transition shadow-md inline-block"
                >
                  Buka di Editor
                </Link>
                <button
                  type="button"
                  disabled={actionLoading}
                  onClick={handleSubmitReview}
                  className="px-3 py-1.5 rounded-lg text-xs font-bold bg-sem-info/15 text-sem-info hover:bg-sem-info/25 border border-sem-info/30 transition cursor-pointer disabled:opacity-50"
                >
                  Ajukan Review
                </button>
              </>
            )}

            {migration.status !== 'DRAFT' && migration.status !== 'PENDING_REVIEW' && migration.status !== 'REJECTED' && (
              <>
                <button
                  type="button"
                  disabled={actionLoading}
                  onClick={handleDuplicate}
                  className="px-3 py-1.5 rounded-lg text-xs font-bold bg-app-active hover:bg-app-border-strong text-white transition cursor-pointer disabled:opacity-50"
                >
                  {actionLoading ? 'Menduplikat...' : 'Duplikat Draft'}
                </button>

                {migration.status === 'SUCCESS' && !migration.is_irreversible && migration.execution_type !== 'ROLLBACK' && (
                  <button
                    type="button"
                    disabled={actionLoading}
                    onClick={handleRollback}
                    className="px-3 py-1.5 rounded-lg text-xs font-bold text-sem-danger hover:bg-sem-danger/15 bg-sem-danger/10 border border-sem-danger/30 transition cursor-pointer disabled:opacity-50"
                  >
                    Rollback
                  </button>
                )}

                {isRolledBack && (
                  <button
                    type="button"
                    disabled={actionLoading}
                    onClick={handleForward}
                    className="px-3 py-1.5 rounded-lg text-xs font-bold text-sem-success hover:bg-sem-success/15 bg-sem-success/10 border border-sem-success/30 transition cursor-pointer disabled:opacity-50"
                  >
                    Forward
                  </button>
                )}
              </>
            )}
          </div>
        )}
      </div>

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

      {/* Rejection notice */}
      {migration.rejection_reason && (
        <div className="p-4 rounded-xl bg-sem-danger/10 border border-sem-danger/30 space-y-1.5 text-xs text-sem-danger">
          <div className="font-bold flex items-center gap-1.5">
            Catatan Penolakan Review
          </div>
          <p className="text-zinc-200 font-mono text-[11px] bg-app-card p-2.5 rounded border border-sem-danger/20">
            {migration.rejection_reason}
          </p>
        </div>
      )}

      {/* Approvals Table */}
      {migration.approvals && migration.approvals.length > 0 && (
        <div className="space-y-2 bg-app-card border border-app-border rounded-xl p-4">
          <h3 className="text-xs font-bold text-zinc-400 uppercase tracking-wider">
            Riwayat Review & Persetujuan
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs text-zinc-300">
              <thead className="text-[10px] uppercase tracking-wider text-zinc-500 border-b border-app-border">
                <tr>
                  <th className="py-2 px-3 font-semibold">Group</th>
                  <th className="py-2 px-3 font-semibold">Reviewer</th>
                  <th className="py-2 px-3 font-semibold">Status</th>
                  <th className="py-2 px-3 font-semibold">Waktu</th>
                  <th className="py-2 px-3 font-semibold">Catatan</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-app-border font-mono text-[11px]">
                {migration.approvals.map((app, idx) => (
                  <tr key={idx}>
                    <td className="py-2 px-3">{app.group_name}</td>
                    <td className="py-2 px-3">{app.leader_name || '-'}</td>
                    <td className="py-2 px-3">
                      {app.status === 'APPROVED' ? (
                        <span className="text-sem-success font-semibold">APPROVED</span>
                      ) : app.status === 'REJECTED' ? (
                        <span className="text-sem-danger font-semibold">REJECTED</span>
                      ) : (
                        <span className="text-sem-warning font-semibold">PENDING</span>
                      )}
                    </td>
                    <td className="py-2 px-3">{fmtTime(app.reviewed_at)}</td>
                    <td className="py-2 px-3 text-zinc-400 font-sans">{app.note || app.rejection_reason || '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Irreversible notice */}
      {migration.is_irreversible && (
        <div className="p-4 rounded-xl bg-sem-warning/10 border border-sem-warning/30 space-y-1.5 text-xs text-sem-warning">
          <div className="font-bold flex items-center gap-2">
            <span>⚠️</span> Migrasi ini ditandai sebagai Irreversible
          </div>
          <p className="text-zinc-300">
            Perubahan skema atau data yang dilakukan oleh migrasi ini tidak dapat dikembalikan secara otomatis.
          </p>
          {migration.irreversible_reason && (
            <div className="mt-2 text-[11px] font-mono bg-app-card p-2.5 rounded border border-sem-warning/20 text-zinc-200">
              <span className="text-sem-warning font-semibold">Alasan: </span>
              {migration.irreversible_reason}
            </div>
          )}
        </div>
      )}

      {/* Metadata Grid */}
      <dl className="grid grid-cols-2 md:grid-cols-4 gap-4 bg-app-card border border-app-border rounded-xl p-5">
        <div>
          <dt className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Database</dt>
          <dd className="mt-0.5 text-xs text-zinc-200 font-mono">{migration.database_name || '-'}</dd>
        </div>
        <div>
          <dt className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Versi</dt>
          <dd className="mt-0.5 text-xs text-zinc-200 font-mono">{versionLabel(migration.version)}</dd>
        </div>
        <div>
          <dt className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Mode Eksekusi</dt>
          <dd className="mt-0.5 text-xs text-zinc-200 font-mono">
            {migration.execution_mode === 'BATCH'
              ? `BATCH (${migration.batch_size || 1000} baris / ${migration.batch_attribute || 'id'})`
              : 'STANDARD (Direct)'}
          </dd>
        </div>
        <div>
          <dt className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Status Rollback</dt>
          <dd className="mt-0.5 text-xs text-zinc-200">
            {migration.is_irreversible ? (
              <span className="text-sem-warning font-semibold">Irreversible</span>
            ) : isRolledBack ? (
              <span className="text-zinc-400">Telah di-rollback</span>
            ) : (
              <span className="text-sem-success">Reversible</span>
            )}
          </dd>
        </div>
        <div>
          <dt className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Dibuat oleh</dt>
          <dd className="mt-0.5 text-xs text-zinc-200 font-mono">{migration.created_by_name || '-'}</dd>
        </div>
        <div>
          <dt className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Dibuat pada</dt>
          <dd className="mt-0.5 text-xs text-zinc-200 font-mono">{fmtTime(migration.created_at)}</dd>
        </div>
        <div>
          <dt className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Dieksekusi oleh</dt>
          <dd className="mt-0.5 text-xs text-zinc-200 font-mono">{migration.executed_by_name || '-'}</dd>
        </div>
        <div>
          <dt className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Dieksekusi pada</dt>
          <dd className="mt-0.5 text-xs text-zinc-200 font-mono">{fmtTime(migration.executed_at)}</dd>
        </div>
        <div>
          <dt className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Durasi</dt>
          <dd className="mt-0.5 text-xs text-zinc-200 font-mono">{`${migration.duration_ms} ms`}</dd>
        </div>
        <div>
          <dt className="text-[10px] font-bold text-zinc-500 uppercase tracking-wider">Status</dt>
          <dd className="mt-0.5 text-xs text-zinc-200 font-mono">{migration.status}</dd>
        </div>
      </dl>

      {migration.status === 'FAILED' && (
        <div className="space-y-2">
          <h3 className="text-xs font-bold text-sem-danger uppercase tracking-wider px-1">
            Error · seluruh script di-ROLLBACK
          </h3>
          <pre className="p-4 rounded-xl bg-sem-danger/10 border border-sem-danger/30 text-sem-danger text-xs font-mono whitespace-pre-wrap">
            {migration.error}
          </pre>
        </div>
      )}

      {migration.status === 'SUCCESS' && (
        <div className="p-3 rounded-lg bg-sem-success/10 border border-sem-success/30 text-sem-success text-xs font-mono">
          Seluruh statement berhasil dan di-COMMIT sebagai {versionLabel(migration.version)}.
        </div>
      )}

      {/* Dual SQL Viewer */}
      <div className="space-y-3">
        <div className="flex items-center justify-between border-b border-app-border">
          <div className="flex space-x-1">
            <button
              type="button"
              onClick={() => setSqlTab('up')}
              className={`px-4 py-2 text-xs font-semibold rounded-t-lg transition flex items-center gap-1.5 cursor-pointer ${
                sqlTab === 'up'
                  ? 'bg-app-card text-brand-primary border-t-2 border-brand-primary border-x border-app-border'
                  : 'text-zinc-400 hover:text-zinc-200 hover:bg-app-active/40'
              }`}
            >
              <span>SQL Up (Apply)</span>
            </button>
            <button
              type="button"
              onClick={() => setSqlTab('down')}
              className={`px-4 py-2 text-xs font-semibold rounded-t-lg transition flex items-center gap-1.5 cursor-pointer ${
                sqlTab === 'down'
                  ? 'bg-app-card text-brand-primary border-t-2 border-brand-primary border-x border-app-border'
                  : 'text-zinc-400 hover:text-zinc-200 hover:bg-app-active/40'
              }`}
            >
              <span>SQL Down (Rollback)</span>
              {migration.is_irreversible && (
                <span className="text-[10px] px-1.5 py-0.2 rounded bg-sem-warning/15 text-sem-warning font-semibold">
                  Irreversible
                </span>
              )}
            </button>
          </div>
          <div className="text-[11px] text-zinc-400 hidden sm:block px-2">
            {sqlTab === 'up'
              ? 'Perubahan skema yang diterapkan'
              : migration.is_irreversible
              ? 'Rollback dinonaktifkan (Irreversible)'
              : 'Perintah pemulihan skema'}
          </div>
        </div>

        {sqlTab === 'up' ? (
          <pre className="p-4 rounded-b-xl bg-app-bg border border-app-border text-sem-info text-xs font-mono whitespace-pre-wrap overflow-x-auto">
            {migration.sql_up || migration.sql || '-- Tidak ada script SQL Up'}
          </pre>
        ) : migration.is_irreversible ? (
          <div className="p-6 bg-app-bg border border-app-border rounded-b-xl space-y-2 text-xs text-zinc-400">
            <div className="text-sem-warning font-semibold">Tidak Ada Script SQL Down</div>
            <p>
              Migrasi ini ditandai sebagai irreversible. Tidak ada script pembatalan (rollback) yang disimpan untuk migrasi ini.
            </p>
          </div>
        ) : (
          <pre className="p-4 rounded-b-xl bg-app-bg border border-app-border text-sem-info text-xs font-mono whitespace-pre-wrap overflow-x-auto">
            {migration.sql_down || '-- Belum ada script SQL Down'}
          </pre>
        )}
      </div>
    </div>
  )
}
