import React, { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api/client'
import { MigrationView } from '../types'
import { useAuth } from '../context/AuthContext'

export const MigrationReviewsPage: React.FC = () => {
  const { user } = useAuth()
  const [reviews, setReviews] = useState<MigrationView[]>([])
  const [loading, setLoading] = useState<boolean>(true)
  const [error, setError] = useState<string | null>(null)
  const [actionLoading, setActionLoading] = useState<boolean>(false)
  const [feedback, setFeedback] = useState<{ success: boolean; message: string } | null>(null)
  const [expandedSQL, setExpandedSQL] = useState<Record<string, boolean>>({})

  // Modal State for Approve/Reject
  const [selectedReview, setSelectedReview] = useState<MigrationView | null>(null)
  const [modalMode, setModalMode] = useState<'APPROVE' | 'REJECT' | null>(null)
  const [modalInput, setModalInput] = useState<string>('')

  const fetchReviews = async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await api.listPendingReviews()
      setReviews(res.reviews || [])
    } catch (err: any) {
      setError(err.data?.error || err.message || 'Gagal memuat daftar review migrasi')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchReviews()
  }, [])

  const toggleSQL = (id: string) => {
    setExpandedSQL((prev) => ({ ...prev, [id]: !prev[id] }))
  }

  const openApproveModal = (m: MigrationView) => {
    setSelectedReview(m)
    setModalMode('APPROVE')
    setModalInput('')
  }

  const openRejectModal = (m: MigrationView) => {
    setSelectedReview(m)
    setModalMode('REJECT')
    setModalInput('')
  }

  const closeModal = () => {
    setSelectedReview(null)
    setModalMode(null)
    setModalInput('')
  }

  const handleSubmitModal = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!selectedReview || !modalMode) return

    if (modalMode === 'REJECT' && !modalInput.trim()) {
      alert('Alasan penolakan wajib diisi.')
      return
    }

    setActionLoading(true)
    setFeedback(null)
    try {
      if (modalMode === 'APPROVE') {
        await api.approveMigration(selectedReview.id, modalInput.trim() || undefined)
        setFeedback({
          success: true,
          message: `Migrasi "${selectedReview.name}" berhasil disetujui.`,
        })
      } else {
        await api.rejectMigration(selectedReview.id, modalInput.trim())
        setFeedback({
          success: true,
          message: `Migrasi "${selectedReview.name}" telah ditolak.`,
        })
      }
      closeModal()
      await fetchReviews()
    } catch (err: any) {
      setFeedback({
        success: false,
        message: err.data?.error || err.message || 'Gagal memproses review',
      })
    } finally {
      setActionLoading(false)
    }
  }

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 pb-4 border-b border-app-border">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-xl font-bold text-zinc-100 tracking-tight">Persetujuan Migrasi</h1>
            <span className="px-2 py-0.5 rounded-full text-[11px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
              {reviews.length} Menunggu
            </span>
          </div>
          <p className="text-zinc-400 text-xs mt-1">
            Daftar pengajuan migrasi skema database yang menunggu peninjauan dan persetujuan Group Leader.
          </p>
        </div>

        <button
          type="button"
          disabled={loading || actionLoading}
          onClick={fetchReviews}
          className="px-3.5 py-1.5 rounded-xl text-xs font-semibold bg-app-surface hover:bg-app-hover border border-app-border text-zinc-300 transition cursor-pointer disabled:opacity-50 inline-flex items-center gap-1.5 self-start sm:self-auto"
        >
          <svg className={`w-3.5 h-3.5 ${loading ? 'animate-spin' : ''}`} fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          Segarkan
        </button>
      </div>

      {feedback && (
        <div
          className={`p-3.5 rounded-xl text-xs font-mono whitespace-pre-wrap border ${
            feedback.success
              ? 'bg-sem-success/10 border-sem-success/30 text-sem-success'
              : 'bg-sem-danger/10 border-sem-danger/30 text-sem-danger'
          }`}
        >
          {feedback.message}
        </div>
      )}

      {error && (
        <div className="p-4 rounded-xl bg-sem-danger/10 border border-sem-danger/30 text-sem-danger text-xs">
          {error}
        </div>
      )}

      {/* Content */}
      {loading && reviews.length === 0 ? (
        <div className="p-12 text-center text-xs text-zinc-400 bg-app-surface rounded-2xl border border-app-border">
          Memuat antrean review migrasi...
        </div>
      ) : reviews.length === 0 ? (
        <div className="p-12 text-center bg-app-surface rounded-2xl border border-app-border space-y-3">
          <div className="w-12 h-12 rounded-2xl bg-app-hover text-zinc-400 flex items-center justify-center mx-auto text-xl">
            <svg className="w-6 h-6 text-zinc-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </div>
          <h2 className="text-sm font-bold text-zinc-200">Tidak Ada Migrasi Menunggu Review</h2>
          <p className="text-zinc-500 text-xs max-w-sm mx-auto">
            Semua pengajuan migrasi skema yang memerlukan persetujuan kelompok Anda telah selesai ditinjau.
          </p>
        </div>
      ) : (
        <div className="space-y-4">
          {reviews.map((m) => {
            const isSQLOpen = Boolean(expandedSQL[m.id])
            const pendingApprovals = (m.approvals || []).filter((a) => a.status === 'PENDING')
            const approvedApprovals = (m.approvals || []).filter((a) => a.status === 'APPROVED')

            return (
              <div
                key={m.id}
                className="bg-app-surface border border-app-border hover:border-app-border-strong rounded-2xl p-5 space-y-4 transition"
              >
                {/* Header Card */}
                <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-3">
                  <div className="space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="px-2.5 py-0.5 rounded-lg text-[10px] font-bold bg-brand-primary/10 text-brand-primary border border-brand-primary/20">
                        {m.database_name || 'Database Target'}
                      </span>
                      <span className="px-2 py-0.5 rounded-md text-[10px] font-bold bg-sem-warning/15 text-sem-warning border border-sem-warning/30">
                        PENDING REVIEW
                      </span>
                      <span className="text-xs text-zinc-500 font-mono">
                        v{m.version || 0}
                      </span>
                    </div>
                    <h3 className="text-base font-bold text-zinc-100 tracking-tight">
                      {m.name}
                    </h3>
                    <div className="text-[11px] text-zinc-400 flex flex-wrap items-center gap-3 pt-0.5">
                      <span>
                        Penulis: <strong className="text-zinc-300 font-medium">{m.created_by_name || '-'}</strong>
                      </span>
                      <span>•</span>
                      <span>
                        Diajukan: <span className="font-mono">{new Date(m.created_at).toLocaleString()}</span>
                      </span>
                    </div>
                  </div>

                  {/* Actions */}
                  <div className="flex flex-wrap items-center gap-2 shrink-0 pt-1 sm:pt-0">
                    {m.can_approve ? (
                      <>
                        <button
                          type="button"
                          disabled={actionLoading}
                          onClick={() => openApproveModal(m)}
                          className="px-3.5 py-1.5 rounded-xl text-xs font-bold bg-sem-success text-app-bg hover:opacity-90 transition cursor-pointer disabled:opacity-50"
                        >
                          Setujui
                        </button>
                        <button
                          type="button"
                          disabled={actionLoading}
                          onClick={() => openRejectModal(m)}
                          className="px-3.5 py-1.5 rounded-lg text-xs font-bold bg-sem-danger/15 text-sem-danger hover:bg-sem-danger/25 border border-sem-danger/30 transition cursor-pointer disabled:opacity-50"
                        >
                          Tolak
                        </button>
                      </>
                    ) : (
                      <span className="px-2.5 py-1 rounded-lg text-[11px] font-medium bg-app-hover text-zinc-400 border border-app-border">
                        {user?.is_platform_admin ? 'Sebagai Admin (Hanya Melihat)' : 'Menunggu review grup lain'}
                      </span>
                    )}

                    <Link
                      to={`/migration/${m.id}`}
                      className="px-3 py-1.5 rounded-xl text-xs font-semibold bg-app-hover hover:bg-app-active text-zinc-300 border border-app-border transition"
                    >
                      Buka Detail
                    </Link>
                  </div>
                </div>

                {/* Approvals status badges */}
                {m.approvals && m.approvals.length > 0 && (
                  <div className="p-3 rounded-xl bg-app-bg/50 border border-app-border text-xs space-y-2">
                    <div className="text-[11px] font-semibold text-zinc-400 uppercase tracking-wider">
                      Status Persetujuan Tim ({approvedApprovals.length}/{m.approvals.length} Disetujui)
                    </div>
                    <div className="flex flex-wrap gap-2">
                      {m.approvals.map((app) => (
                        <div
                          key={app.id || app.group_id}
                          className={`px-2.5 py-1 rounded-lg text-[11px] border flex items-center gap-1.5 ${
                            app.status === 'APPROVED'
                              ? 'bg-sem-success/10 border-sem-success/30 text-sem-success'
                              : app.status === 'REJECTED'
                              ? 'bg-sem-danger/10 border-sem-danger/30 text-sem-danger'
                              : 'bg-sem-warning/10 border-sem-warning/30 text-sem-warning'
                          }`}
                        >
                          <span className="font-semibold">{app.group_name}</span>
                          <span className="text-[10px] opacity-75">
                            ({app.status === 'APPROVED' ? 'Disetujui' : app.status === 'REJECTED' ? 'Ditolak' : 'Menunggu'})
                          </span>
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {/* SQL Script Viewer */}
                <div className="space-y-1.5 pt-1">
                  <div className="flex items-center justify-between">
                    <button
                      type="button"
                      onClick={() => toggleSQL(m.id)}
                      className="text-xs font-semibold text-brand-primary hover:underline cursor-pointer flex items-center gap-1"
                    >
                      <span>{isSQLOpen ? 'Sembunyikan Script SQL' : 'Lihat Script SQL'}</span>
                      <svg
                        className={`w-3.5 h-3.5 transition-transform ${isSQLOpen ? 'rotate-180' : ''}`}
                        fill="none"
                        stroke="currentColor"
                        viewBox="0 0 24 24"
                      >
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M19 9l-7 7-7-7" />
                      </svg>
                    </button>
                    <span className="text-[10px] font-mono text-zinc-500">
                      {m.sql ? `${m.sql.split('\n').length} baris` : '0 baris'}
                    </span>
                  </div>

                  {isSQLOpen && (
                    <pre className="p-3.5 rounded-xl bg-app-bg border border-app-border text-xs font-mono text-zinc-200 overflow-x-auto max-h-64 leading-relaxed">
                      {m.sql || '-- Tidak ada script SQL'}
                    </pre>
                  )}
                </div>
              </div>
            )
          })}
        </div>
      )}

      {/* Review Confirmation Modal */}
      {selectedReview && modalMode && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-xs">
          <div className="bg-app-surface border border-app-border rounded-2xl w-full max-w-md p-6 space-y-4 shadow-2xl">
            <div>
              <div className="flex items-center justify-between">
                <h3 className="text-base font-bold text-zinc-100">
                  {modalMode === 'APPROVE' ? 'Setujui Migrasi' : 'Tolak Migrasi'}
                </h3>
                <button
                  type="button"
                  onClick={closeModal}
                  className="text-zinc-400 hover:text-zinc-200 p-1 rounded-lg cursor-pointer"
                >
                  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M6 18L18 6M6 6l12 12" />
                  </svg>
                </button>
              </div>
              <p className="text-zinc-400 text-xs mt-1">
                {modalMode === 'APPROVE'
                  ? `Konfirmasi persetujuan untuk migrasi "${selectedReview.name}".`
                  : `Berikan alasan penolakan untuk migrasi "${selectedReview.name}".`}
              </p>
            </div>

            <form onSubmit={handleSubmitModal} className="space-y-4">
              <div>
                <label className="block text-xs font-semibold text-zinc-300 mb-1.5">
                  {modalMode === 'APPROVE' ? 'Catatan Persetujuan (Opsional)' : 'Alasan Penolakan (Wajib)'}
                </label>
                <textarea
                  rows={3}
                  value={modalInput}
                  onChange={(e) => setModalInput(e.target.value)}
                  placeholder={
                    modalMode === 'APPROVE'
                      ? 'Contoh: Sudah diverifikasi dan aman dijalankan.'
                      : 'Contoh: Indeks pada tabel users berpotensi menyebabkan lock panjang.'
                  }
                  required={modalMode === 'REJECT'}
                  className="w-full px-3.5 py-2.5 rounded-xl bg-app-bg border border-app-border focus:border-brand-primary text-xs font-sans text-zinc-100 outline-none transition"
                />
              </div>

              <div className="flex items-center justify-end gap-2 pt-2">
                <button
                  type="button"
                  disabled={actionLoading}
                  onClick={closeModal}
                  className="px-4 py-2 rounded-xl text-xs font-semibold text-zinc-400 hover:text-zinc-200 hover:bg-app-hover transition cursor-pointer"
                >
                  Batal
                </button>
                <button
                  type="submit"
                  disabled={actionLoading}
                  className={`px-4 py-2 rounded-xl text-xs font-bold transition cursor-pointer disabled:opacity-50 ${
                    modalMode === 'APPROVE'
                      ? 'bg-sem-success text-app-bg hover:opacity-90'
                      : 'bg-sem-danger text-white hover:opacity-90'
                  }`}
                >
                  {actionLoading
                    ? 'Memproses...'
                    : modalMode === 'APPROVE'
                    ? 'Konfirmasi Setujui'
                    : 'Konfirmasi Tolak'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  )
}
