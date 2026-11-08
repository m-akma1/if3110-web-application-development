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

  useEffect(() => {
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

  return (
    <div className="space-y-5 max-w-5xl">
      <Link to="/migration" className="text-xs font-semibold text-brand-primary hover:underline">
        ← Kembali ke Migration Editor
      </Link>

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="flex items-center gap-3">
            <h1 className="text-2xl font-bold text-zinc-100">{migration.name}</h1>
            {migration.status === 'SUCCESS' ? (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-success/15 text-sem-success border border-sem-success/30">
                SUCCESS
              </span>
            ) : migration.status === 'FAILED' ? (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-danger/15 text-sem-danger border border-sem-danger/30">
                FAILED
              </span>
            ) : (
              <span className="px-2 py-0.5 rounded text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
                DRAFT
              </span>
            )}
          </div>
          <p className="text-zinc-400 mt-1 text-xs font-mono">{migration.id}</p>
        </div>

        {canEdit && (
          <div>
            {migration.status === 'DRAFT' ? (
              <Link
                to={`/migration?draft=${migration.id}`}
                className="px-4 py-1.5 rounded-lg text-xs font-bold bg-brand-primary hover:bg-brand-primary-hover text-app-bg transition shadow-md inline-block"
              >
                Buka di Editor
              </Link>
            ) : (
              <button
                type="button"
                disabled={actionLoading}
                onClick={handleDuplicate}
                className="px-4 py-1.5 rounded-lg text-xs font-bold bg-app-active hover:bg-app-border-strong text-white transition cursor-pointer disabled:opacity-50"
              >
                {actionLoading ? 'Menduplikat...' : 'Duplikat sebagai Draft Baru'}
              </button>
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
          {feedback.success ? `✓ ${feedback.message}` : `✗ ${feedback.message}`}
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
          ✓ Seluruh statement berhasil dan di-COMMIT sebagai {versionLabel(migration.version)}.
        </div>
      )}

      <div className="space-y-2">
        <h3 className="text-xs font-bold text-zinc-400 uppercase tracking-wider px-1">Script SQL</h3>
        <pre className="p-4 rounded-xl bg-app-bg border border-app-border text-sem-info text-xs font-mono whitespace-pre-wrap overflow-x-auto">
          {migration.sql}
        </pre>
      </div>
    </div>
  )
}
