import React from 'react'
import { Link } from 'react-router-dom'
import { MigrationView, DryRunView, User, GroupSummary } from '../../types'
import { DryRunReport } from '../DryRunReport'

interface MigrationEditorProps {
  selectedDraft: MigrationView | null
  user: User | null
  groups: GroupSummary[]
  name: string
  setName: (v: string) => void
  sqlTab: 'up' | 'down'
  setSqlTab: (v: 'up' | 'down') => void
  sqlUp: string
  setSqlUp: (v: string) => void
  sqlDown: string
  setSqlDown: (v: string) => void
  isIrreversible: boolean
  setIsIrreversible: (v: boolean) => void
  irreversibleReason: string
  setIrreversibleReason: (v: string) => void
  executionMode: 'STANDARD' | 'BATCH'
  setExecutionMode: (v: 'STANDARD' | 'BATCH') => void
  batchAttribute: string
  setBatchAttribute: (v: string) => void
  batchSize: number
  setBatchSize: (v: number) => void
  isLocked: boolean
  actionLoading: boolean
  feedback: { success: boolean; message: string } | null
  dryRunReport: DryRunView | null
  onFileUpload: (e: React.ChangeEvent<HTMLInputElement>) => void
  onSaveDraft: () => void
  onDeleteDraft: () => void
  onDryRun: () => void
  onSubmitReview: () => void
  onExecute: () => void
}

export const MigrationEditor: React.FC<MigrationEditorProps> = ({
  selectedDraft,
  user,
  groups,
  name,
  setName,
  sqlTab,
  setSqlTab,
  sqlUp,
  setSqlUp,
  sqlDown,
  setSqlDown,
  isIrreversible,
  setIsIrreversible,
  irreversibleReason,
  setIrreversibleReason,
  executionMode,
  setExecutionMode,
  batchAttribute,
  setBatchAttribute,
  batchSize,
  setBatchSize,
  isLocked,
  actionLoading,
  feedback,
  dryRunReport,
  onFileUpload,
  onSaveDraft,
  onDeleteDraft,
  onDryRun,
  onSubmitReview,
  onExecute,
}) => {
  return (
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
            disabled={isLocked || actionLoading}
            onChange={(e) => setName(e.target.value)}
            placeholder="Nama migration, mis. tambah tabel orders"
            className="flex-1 min-w-[12rem] px-3 py-2 bg-app-bg border border-app-border rounded-lg text-xs text-zinc-100 focus:outline-none focus:ring-2 focus:ring-brand-primary/50 disabled:opacity-75 disabled:cursor-not-allowed"
          />
          {!isLocked && (
            <label className="px-3 py-2 rounded-lg text-xs font-medium bg-app-active hover:bg-app-border-strong text-zinc-200 cursor-pointer transition">
              Muat file .sql
              <input
                type="file"
                accept=".sql,text/plain"
                className="hidden"
                onChange={onFileUpload}
              />
            </label>
          )}
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
              readOnly={isLocked}
              value={sqlUp}
              onChange={(e) => setSqlUp(e.target.value)}
              placeholder="-- Tulis perintah SQL perubahan skema di sini (SQL Up). Misal: CREATE TABLE users (...); ALTER TABLE orders ADD COLUMN..."
              className="w-full p-4 bg-app-bg border border-app-border rounded-b-lg font-mono text-xs text-sem-info focus:outline-none focus:ring-2 focus:ring-brand-primary/50 leading-relaxed resize-y read-only:opacity-85"
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
              readOnly={isLocked}
              value={sqlDown}
              onChange={(e) => setSqlDown(e.target.value)}
              placeholder="-- Tulis perintah SQL untuk mengembalikan perubahan (SQL Down). Misal: DROP TABLE users; ALTER TABLE orders DROP COLUMN..."
              className="w-full p-4 bg-app-bg border border-app-border rounded-b-lg font-mono text-xs text-sem-info focus:outline-none focus:ring-2 focus:ring-brand-primary/50 leading-relaxed resize-y read-only:opacity-85"
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
                  disabled={isLocked}
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
                  readOnly={isLocked}
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
                Menunggu Review Group Leader
              </div>
              <p className="text-zinc-300 text-[11px] mt-0.5">
                Migrasi ini diajukan untuk peninjauan dan memerlukan persetujuan Group Leader sebelum dapat dieksekusi.
              </p>
            </div>
            {(user?.is_platform_admin || user?.is_leader || groups?.some((g) => g.is_leader)) && (
              <Link
                to="/migration/reviews"
                className="px-3 py-1.5 rounded-lg text-xs font-semibold bg-sem-warning/20 hover:bg-sem-warning/30 text-sem-warning border border-sem-warning/40 transition shrink-0 inline-flex items-center gap-1.5"
              >
                Buka Halaman Persetujuan →
              </Link>
            )}
          </div>
        )}

        {selectedDraft && selectedDraft.status === 'APPROVED' && (
          <div className="p-3.5 rounded-xl bg-sem-success/10 border border-sem-success/30 text-xs text-sem-success flex items-center justify-between gap-3">
            <div>
              <div className="font-bold">Migrasi Telah Disetujui (Approved)</div>
              <p className="text-zinc-300 text-[11px] mt-0.5">
                Semua Group Leader telah menyetujui pengajuan migrasi ini. Script telah dikunci dan siap dieksekusi ke database target.
              </p>
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
                onClick={onSaveDraft}
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
                onClick={onDeleteDraft}
                className="px-3 py-1.5 rounded-lg text-xs font-medium text-sem-danger hover:bg-sem-danger/10 transition cursor-pointer disabled:opacity-50"
              >
                Hapus Draft
              </button>
              <div className="flex flex-wrap gap-2">
                {!isLocked && (
                  <button
                    type="button"
                    disabled={actionLoading}
                    onClick={onSaveDraft}
                    className="px-3 py-1.5 rounded-lg text-xs font-medium bg-app-active hover:bg-app-border-strong text-white transition cursor-pointer disabled:opacity-50"
                  >
                    Simpan
                  </button>
                )}
                <button
                  type="button"
                  disabled={actionLoading}
                  onClick={onDryRun}
                  className="px-3 py-1.5 rounded-lg text-xs font-medium bg-app-active hover:bg-app-border-strong text-white transition cursor-pointer disabled:opacity-50"
                >
                  Dry Run + EXPLAIN
                </button>
                {(!selectedDraft.status || selectedDraft.status === 'DRAFT' || selectedDraft.status === 'REJECTED') && (
                  <button
                    type="button"
                    disabled={actionLoading}
                    onClick={onSubmitReview}
                    className="px-3 py-1.5 rounded-lg text-xs font-semibold bg-sem-info/15 hover:bg-sem-info/25 text-sem-info border border-sem-info/30 transition cursor-pointer disabled:opacity-50"
                    title="Ajukan migrasi ini untuk direview oleh Group Leader"
                  >
                    Ajukan Review
                  </button>
                )}
                <button
                  type="button"
                  disabled={actionLoading || selectedDraft.status === 'PENDING_REVIEW' || selectedDraft.status === 'REJECTED'}
                  onClick={onExecute}
                  title={
                    selectedDraft.status === 'PENDING_REVIEW'
                      ? 'Migrasi masih menunggu persetujuan Group Leader'
                      : selectedDraft.status === 'REJECTED'
                      ? 'Migrasi ditolak. Silakan revisi lalu ajukan review ulang.'
                      : undefined
                  }
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
  )
}
