import React, { useEffect, useState } from 'react'
import { api } from '../../api/client'
import { MigrationExecutionView } from '../../types'
import { fmtTime } from '../../utils/migrationFormat'

const statusClass: Record<string, string> = {
  SUCCESS: 'text-sem-success',
  FAILED: 'text-sem-danger',
  UNCERTAIN: 'text-sem-warning',
  PARTIAL: 'text-sem-warning',
}

// ExecutionHistory lists every attempt to run one migration (append-only history), including
// attempts that failed before any SQL reached the target. refreshKey reloads the list, e.g.
// when the migration's status changes after a job finishes.
export const ExecutionHistory: React.FC<{ migrationId: string; refreshKey?: string }> = ({ migrationId, refreshKey }) => {
  const [executions, setExecutions] = useState<MigrationExecutionView[]>([])
  const [open, setOpen] = useState<string | null>(null)

  useEffect(() => {
    api
      .getMigrationExecutions(migrationId)
      .then((res) => setExecutions(res.executions || []))
      .catch(() => setExecutions([]))
  }, [migrationId, refreshKey])

  if (executions.length === 0) return null
  return (
    <div className="space-y-2 bg-app-card border border-app-border rounded-xl p-4">
      <h3 className="text-xs font-bold text-zinc-400 uppercase tracking-wider">Riwayat Eksekusi</h3>
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs text-zinc-300">
          <thead className="text-[10px] uppercase tracking-wider text-zinc-500 border-b border-app-border">
            <tr>
              <th className="py-2 px-3 font-semibold">Execution ID</th>
              <th className="py-2 px-3 font-semibold">Jenis</th>
              <th className="py-2 px-3 font-semibold">Attempt</th>
              <th className="py-2 px-3 font-semibold">Status</th>
              <th className="py-2 px-3 font-semibold">Oleh</th>
              <th className="py-2 px-3 font-semibold">Selesai</th>
              <th className="py-2 px-3 font-semibold">Keterangan</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-app-border font-mono text-[11px]">
            {executions.map((ex) => (
              <React.Fragment key={ex.id}>
                <tr className="cursor-pointer hover:bg-app-active/30" onClick={() => setOpen(open === ex.id ? null : ex.id)}>
                  <td className="py-2 px-3" title={ex.id}>{ex.id.slice(0, 8)}</td>
                  <td className="py-2 px-3">
                    {ex.execution_type}
                    {ex.reverts_execution_id && (
                      <span className="text-zinc-500" title={ex.reverts_execution_id}> ↩ {ex.reverts_execution_id.slice(0, 8)}</span>
                    )}
                  </td>
                  <td className="py-2 px-3">{ex.attempt}</td>
                  <td className={`py-2 px-3 font-semibold ${statusClass[ex.status] || ''}`}>{ex.status}</td>
                  <td className="py-2 px-3">{ex.executor_name || '-'}</td>
                  <td className="py-2 px-3">{fmtTime(ex.finished_at)}</td>
                  <td className="py-2 px-3 text-zinc-400 font-sans">
                    {ex.sql_sent ? `${ex.duration_ms} ms` : 'Gagal sebelum SQL dikirim'}
                    {ex.error && <span className="text-sem-danger"> · {ex.error.split('\n')[0]}</span>}
                  </td>
                </tr>
                {open === ex.id && (
                  <tr>
                    <td colSpan={7} className="px-3 pb-3">
                      <pre className="p-3 rounded bg-app-bg border border-app-border text-sem-info whitespace-pre-wrap overflow-x-auto">
                        {ex.sql_sent ? ex.sql_executed : '-- Tidak ada SQL yang dikirim ke database target'}
                      </pre>
                      {ex.job_id && <div className="mt-1 text-zinc-500">job {ex.job_id} · correlation {ex.correlation_id || '-'}</div>}
                    </td>
                  </tr>
                )}
              </React.Fragment>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
