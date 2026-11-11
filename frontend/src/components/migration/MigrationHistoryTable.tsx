import React from 'react'
import { Link } from 'react-router-dom'
import { MigrationView } from '../../types'
import { fmtTime, versionLabel } from '../../utils/migrationFormat'

interface MigrationHistoryTableProps {
  history: MigrationView[]
  filteredHistory: MigrationView[]
  hideRollback: boolean
  setHideRollback: (v: boolean) => void
  canEdit: boolean
  actionLoading: boolean
  onRollback: (m: MigrationView) => void
  onForward: (m: MigrationView) => void
  onResumeBatch: (m: MigrationView) => void
  onAbortBatch: (m: MigrationView) => void
}

export const MigrationHistoryTable: React.FC<MigrationHistoryTableProps> = ({
  history,
  filteredHistory,
  hideRollback,
  setHideRollback,
  canEdit,
  actionLoading,
  onRollback,
  onForward,
  onResumeBatch,
  onAbortBatch,
}) => {
  return (
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
              filteredHistory.map((h) => (
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
                              onClick={() => onResumeBatch(h)}
                              className="px-2.5 py-1 rounded text-[11px] font-semibold text-app-bg bg-brand-primary hover:bg-brand-primary-hover transition cursor-pointer disabled:opacity-50"
                              title="Lanjutkan eksekusi dari checkpoint terakhir"
                            >
                              Lanjutkan
                            </button>
                            <button
                              type="button"
                              disabled={actionLoading}
                              onClick={() => onAbortBatch(h)}
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
                            disabled={actionLoading || h.can_rollback === false}
                            onClick={() => onRollback(h)}
                            className="px-2.5 py-1 rounded text-[11px] font-semibold text-sem-danger hover:bg-sem-danger/10 border border-sem-danger/30 transition cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                            title={
                              h.can_rollback === false
                                ? 'Rollback hanya dapat dilakukan pada migration aktif terakhir (urutan LIFO)'
                                : 'Ajukan atau jalankan rollback (SQL Down)'
                            }
                          >
                            Rollback
                          </button>
                        )}
                        {h.status === 'ROLLED_BACK' && h.execution_type !== 'ROLLBACK' && h.can_forward && (
                          <button
                            type="button"
                            disabled={actionLoading}
                            onClick={() => onForward(h)}
                            className="px-2.5 py-1 rounded text-[11px] font-semibold text-sem-success hover:bg-sem-success/10 border border-sem-success/30 transition cursor-pointer disabled:opacity-50"
                            title="Ajukan atau jalankan forward (mengaplikasikan kembali SQL Up)"
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
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
