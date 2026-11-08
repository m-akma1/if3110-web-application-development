import React from 'react'
import { DryRunView } from '../types'

function fmtMs(ms: number): string {
  if (ms < 1) {
    return `${ms.toFixed(3)} ms`
  }
  return `${ms.toFixed(1)} ms`
}

function fmtRows(r: number): string {
  if (r === Math.floor(r)) {
    return `${Math.floor(r)}`
  }
  return r.toFixed(2)
}

function stepBadgeClass(kind: string): string {
  switch (kind) {
    case 'EXPLAINED':
      return 'bg-sem-info/15 text-sem-info border-sem-info/30'
    case 'FAILED':
      return 'bg-sem-danger/15 text-sem-danger border-sem-danger/30'
    case 'SKIPPED':
      return 'bg-zinc-500/10 text-zinc-500 border-zinc-500/30'
    default:
      return 'bg-sem-success/10 text-sem-success border-sem-success/30'
  }
}

function stepLabel(kind: string): string {
  switch (kind) {
    case 'EXPLAINED':
      return 'EXPLAIN ANALYZE'
    case 'EXECUTED':
      return 'DIJALANKAN · tidak dapat di-EXPLAIN'
    case 'FAILED':
      return 'GAGAL'
    case 'SKIPPED':
      return 'DILEWATI'
    default:
      return kind
  }
}

export const DryRunReport: React.FC<{ report: DryRunView }> = ({ report }) => {
  return (
    <div className="space-y-3">
      {report.ok ? (
        <div className="p-3 rounded-lg bg-sem-success/10 border border-sem-success/30 text-sem-success text-xs font-mono">
          ✓ Dry run berhasil: {report.statements} statement dijalankan dalam {fmtMs(report.duration_ms)}, lalu di-ROLLBACK. Database target tidak berubah.
        </div>
      ) : (
        <div className="p-3 rounded-lg bg-sem-danger/10 border border-sem-danger/30 text-sem-danger text-xs font-mono whitespace-pre-wrap">
          ✗ Dry run gagal (seluruh perubahan di-ROLLBACK):{'\n'}
          {report.error}
        </div>
      )}

      {report.steps.map((st) => (
        <div key={st.index} className="bg-app-bg border border-app-border rounded-lg overflow-hidden">
          <div className="flex items-start justify-between gap-3 px-3 py-2 border-b border-app-border">
            <pre className="font-mono text-[11px] text-zinc-300 whitespace-pre-wrap break-all flex-1">
              <span className="text-zinc-500">{`#${st.index}  `}</span>
              {st.sql}
            </pre>
            <div className="flex items-center gap-2 shrink-0">
              {st.kind !== 'SKIPPED' && (
                <span className="text-[10px] font-mono text-zinc-500">{fmtMs(st.duration_ms)}</span>
              )}
              <span className={`px-2 py-0.5 rounded text-[10px] font-bold border ${stepBadgeClass(st.kind)}`}>
                {stepLabel(st.kind)}
              </span>
            </div>
          </div>

          {st.nodes && st.nodes.length > 0 && (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-[11px] text-zinc-300">
                <thead className="text-[10px] uppercase tracking-wider text-zinc-500 border-b border-app-border">
                  <tr>
                    <th className="px-3 py-1.5 font-semibold">Node</th>
                    <th className="px-3 py-1.5 font-semibold text-right">Rows est.</th>
                    <th className="px-3 py-1.5 font-semibold text-right">Rows actual</th>
                    <th className="px-3 py-1.5 font-semibold text-right">Loops</th>
                    <th className="px-3 py-1.5 font-semibold text-right">Cost</th>
                    <th className="px-3 py-1.5 font-semibold text-right">Actual time</th>
                    <th className="px-3 py-1.5 font-semibold text-right">Buffers hit/read</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-app-border/60">
                  {st.nodes.map((n, nIdx) => (
                    <tr key={nIdx} className={n.never_executed ? 'opacity-50' : ''}>
                      <td className="py-1.5 pr-3" style={{ paddingLeft: `${12 + n.depth * 20}px` }}>
                        <div className="font-mono font-semibold text-zinc-100">
                          {n.depth > 0 && <span className="text-zinc-600">→ </span>}
                          {n.title}
                          {n.join_type && (
                            <span className="ml-1 text-[10px] text-brand-primary">{n.join_type} join</span>
                          )}
                        </div>
                        {n.conditions && n.conditions.length > 0 && (
                          <div className="text-[10px] text-zinc-400 font-mono mt-0.5">
                            {n.conditions.join(' · ')}
                          </div>
                        )}
                        {n.rows_removed > 0 && (
                          <div className="text-[10px] text-zinc-500 font-mono">
                            {n.rows_removed} baris difilter
                          </div>
                        )}
                      </td>
                      <td className="px-3 py-1.5 text-right font-mono text-zinc-400">
                        {fmtRows(n.est_rows)}
                      </td>
                      <td className="px-3 py-1.5 text-right font-mono">
                        {n.never_executed ? (
                          <span className="text-zinc-500">tidak dijalankan</span>
                        ) : (
                          <span className={n.misestimated ? 'text-sem-warning font-semibold' : 'text-zinc-200'}>
                            {fmtRows(n.actual_rows)}
                          </span>
                        )}
                      </td>
                      <td className="px-3 py-1.5 text-right font-mono text-zinc-400">
                        {n.loops}
                      </td>
                      <td className="px-3 py-1.5 text-right font-mono text-zinc-400">
                        {n.total_cost ? n.total_cost.toFixed(1) : '-'}
                      </td>
                      <td className="px-3 py-1.5 text-right font-mono text-zinc-200">
                        {n.actual_ms ? fmtMs(n.actual_ms) : '-'}
                      </td>
                      <td className="px-3 py-1.5 text-right font-mono text-zinc-400">
                        {n.shared_hit || n.shared_read ? `${n.shared_hit} / ${n.shared_read}` : '-'}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      ))}
    </div>
  )
}
