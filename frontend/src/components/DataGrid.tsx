import React, { useState } from 'react'
import { TableDataResult } from '../types'

export const DataGrid: React.FC<{
  data: TableDataResult
  onPageChange?: (page: number) => void
}> = ({ data, onPageChange }) => {
  const [activeView, setActiveView] = useState<'data' | 'schema'>('data')
  const [copied, setCopied] = useState(false)

  const handleCopy = () => {
    if (!data.columns || !data.rows) return
    const headerRow = data.columns.join('\t')
    const rowLines = data.rows.map((row) => row.map((cell) => cell ?? '').join('\t'))
    const fullText = [headerRow, ...rowLines].join('\n')

    navigator.clipboard.writeText(fullText).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    })
  }

  return (
    <div id="data-grid-container" className="space-y-4">
      {data.query_error && (
        <div className="p-3.5 rounded-xl bg-sem-danger/10 border border-sem-danger/30 text-sem-danger text-xs font-medium flex items-center gap-2">
          <svg className="w-4 h-4 shrink-0" viewBox="0 0 20 20" fill="currentColor">
            <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-5a.75.75 0 01.75.75v4.5a.75.75 0 01-1.5 0v-4.5A.75.75 0 0110 5zm0 10a1 1 0 100-2 1 1 0 000 2z" clipRule="evenodd" />
          </svg>
          <span>{data.query_error}</span>
        </div>
      )}

      {data.executed_query && !data.query_error && (
        <div className="p-3 rounded-xl bg-sem-success/10 border border-sem-success/30 text-sem-success text-xs flex items-center justify-between gap-2">
          <span>
            Query Read-Only berhasil dieksekusi: <code className="font-mono text-white">{data.executed_query}</code>
          </span>
          <span className="text-[10px] uppercase font-bold tracking-wider px-2 py-0.5 rounded bg-sem-success/20">
            Read-Only Tx
          </span>
        </div>
      )}

      {data.truncated && !data.query_error && (
        <div className="p-3 rounded-xl bg-sem-warning/10 border border-sem-warning/30 text-sem-warning text-xs">
          Hasil dipotong: hanya {data.rows.length} baris pertama yang ditampilkan. Tambahkan LIMIT atau filter WHERE untuk mempersempit hasil.
        </div>
      )}

      {/* Header Tabel & Tab Switcher */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2.5">
          {data.is_query_result ? (
            <h2 className="text-lg font-bold text-white flex items-center gap-1.5">
              <span>Hasil Query</span>
              {data.has_schema && (
                <>
                  <span className="text-xs font-normal text-zinc-400">dari</span>
                  <span className="text-zinc-400 font-mono text-sm">{data.schema_name}.</span>
                  <span className="text-brand-primary font-mono text-sm">{data.table_name}</span>
                </>
              )}
            </h2>
          ) : (
            <h2 className="text-lg font-bold text-white flex items-center gap-1.5">
              <span className="text-zinc-400 font-mono text-sm">{data.schema_name}.</span>
              <span className="text-brand-primary font-mono">{data.table_name}</span>
            </h2>
          )}
          <span className="text-xs text-zinc-400 bg-app-card px-2.5 py-1 rounded-lg border border-app-border">
            {data.total_rows} Baris
          </span>
          {data.rows && data.rows.length > 0 && (
            <button
              type="button"
              onClick={handleCopy}
              className="text-xs text-zinc-300 bg-app-card hover:bg-app-active px-2.5 py-1 rounded-lg border border-app-border transition cursor-pointer"
              title="Salin baris yang ditampilkan sebagai teks (dipisah tab)"
            >
              <span>{copied ? 'Tersalin' : 'Salin'}</span>
            </button>
          )}
        </div>

        {data.has_schema && (
          <div className="inline-flex rounded-lg bg-app-bg p-1 border border-app-border">
            <button
              type="button"
              onClick={() => setActiveView('data')}
              className={`px-3 py-1.5 rounded-md text-xs font-semibold transition cursor-pointer ${
                activeView === 'data' ? 'bg-brand-primary text-app-bg shadow' : 'text-zinc-400 hover:text-zinc-200'
              }`}
            >
              Data Tabel
            </button>
            <button
              type="button"
              onClick={() => setActiveView('schema')}
              className={`px-3 py-1.5 rounded-md text-xs font-semibold transition cursor-pointer flex items-center gap-1.5 ${
                activeView === 'schema' ? 'bg-brand-primary text-app-bg shadow' : 'text-zinc-400 hover:text-zinc-200'
              }`}
            >
              Schema Viewer
            </button>
          </div>
        )}
      </div>

      {activeView === 'data' ? (
        <div className="space-y-3">
          <div className="bg-app-card border border-app-border rounded-xl overflow-x-auto shadow-xl">
            <table className="w-full text-left text-sm text-zinc-300">
              <thead className="bg-app-input text-xs uppercase tracking-wider text-zinc-400 border-b border-app-border">
                <tr>
                  {data.columns.map((col) => (
                    <th key={col} className="px-4 py-3 font-semibold whitespace-nowrap">
                      {col}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-app-border font-mono text-xs text-zinc-200">
                {data.rows.length === 0 ? (
                  <tr>
                    <td colSpan={data.columns.length || 1} className="px-4 py-8 text-center text-zinc-500">
                      Tidak ada baris data.
                    </td>
                  </tr>
                ) : (
                  data.rows.map((row, idx) => (
                    <tr key={idx} className="hover:bg-app-active/40 transition">
                      {row.map((val, cellIdx) => (
                        <td key={cellIdx} className="px-4 py-2.5 whitespace-nowrap">
                          {val === null || val === undefined ? (
                            <span className="text-zinc-600 italic">NULL</span>
                          ) : (
                            val
                          )}
                        </td>
                      ))}
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>

          {/* Pagination Controls */}
          {!data.is_query_result && data.total_pages > 1 && (
            <div className="flex items-center justify-between text-xs text-zinc-400 px-1">
              <span>
                Halaman {data.current_page} dari {data.total_pages}
              </span>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  disabled={data.current_page <= 1}
                  onClick={() => onPageChange && onPageChange(data.current_page - 1)}
                  className="px-3 py-1.5 rounded-lg border border-app-border bg-app-card hover:bg-app-active disabled:opacity-40 disabled:cursor-not-allowed transition cursor-pointer"
                >
                  Sebelumnya
                </button>
                <button
                  type="button"
                  disabled={data.current_page >= data.total_pages}
                  onClick={() => onPageChange && onPageChange(data.current_page + 1)}
                  className="px-3 py-1.5 rounded-lg border border-app-border bg-app-card hover:bg-app-active disabled:opacity-40 disabled:cursor-not-allowed transition cursor-pointer"
                >
                  Selanjutnya
                </button>
              </div>
            </div>
          )}
        </div>
      ) : (
        /* Schema Viewer */
        <div className="space-y-6">
          {/* Columns */}
          <div className="bg-app-card border border-app-border rounded-xl overflow-hidden shadow-xl">
            <div className="px-4 py-3 bg-app-input border-b border-app-border font-bold text-xs text-white uppercase tracking-wider">
              Kolom & Tipe Data
            </div>
            <div className="overflow-x-auto">
              <table className="w-full text-left text-xs text-zinc-300">
                <thead className="bg-app-input/50 text-[10px] uppercase tracking-wider text-zinc-400 border-b border-app-border">
                  <tr>
                    <th className="px-4 py-2 font-semibold">Kolom</th>
                    <th className="px-4 py-2 font-semibold">Tipe Data</th>
                    <th className="px-4 py-2 font-semibold">Nullable</th>
                    <th className="px-4 py-2 font-semibold">Default</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-app-border font-mono">
                  {data.columns_meta?.map((col) => (
                    <tr key={col.name}>
                      <td className="px-4 py-2 font-semibold text-zinc-100 flex items-center gap-1.5">
                        {col.is_pk && (
                          <span className="px-1.5 py-0.5 rounded text-[9px] font-bold bg-brand-primary text-app-bg">
                            PK
                          </span>
                        )}
                        <span>{col.name}</span>
                      </td>
                      <td className="px-4 py-2 text-sem-info">{col.data_type}</td>
                      <td className="px-4 py-2">{col.is_nullable ? 'YES' : 'NO'}</td>
                      <td className="px-4 py-2 text-zinc-400">{col.default_value || '-'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>

          {/* Constraints */}
          {data.constraints && data.constraints.length > 0 && (
            <div className="bg-app-card border border-app-border rounded-xl overflow-hidden shadow-xl">
              <div className="px-4 py-3 bg-app-input border-b border-app-border font-bold text-xs text-white uppercase tracking-wider">
                Constraints
              </div>
              <div className="overflow-x-auto">
                <table className="w-full text-left text-xs text-zinc-300">
                  <thead className="bg-app-input/50 text-[10px] uppercase tracking-wider text-zinc-400 border-b border-app-border">
                    <tr>
                      <th className="px-4 py-2 font-semibold">Nama Constraint</th>
                      <th className="px-4 py-2 font-semibold">Tipe</th>
                      <th className="px-4 py-2 font-semibold">Definisi</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-app-border font-mono">
                    {data.constraints.map((c) => (
                      <tr key={c.name}>
                        <td className="px-4 py-2 font-semibold text-zinc-200">{c.name}</td>
                        <td className="px-4 py-2">
                          <span className="px-1.5 py-0.5 rounded text-[10px] bg-app-input border border-app-border text-zinc-300">
                            {c.type}
                          </span>
                        </td>
                        <td className="px-4 py-2 text-zinc-400">{c.definition}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* Indexes */}
          {data.indexes && data.indexes.length > 0 && (
            <div className="bg-app-card border border-app-border rounded-xl overflow-hidden shadow-xl">
              <div className="px-4 py-3 bg-app-input border-b border-app-border font-bold text-xs text-white uppercase tracking-wider">
                Indexes
              </div>
              <div className="overflow-x-auto">
                <table className="w-full text-left text-xs text-zinc-300">
                  <thead className="bg-app-input/50 text-[10px] uppercase tracking-wider text-zinc-400 border-b border-app-border">
                    <tr>
                      <th className="px-4 py-2 font-semibold">Nama Index</th>
                      <th className="px-4 py-2 font-semibold">Kolom</th>
                      <th className="px-4 py-2 font-semibold">Tipe</th>
                      <th className="px-4 py-2 font-semibold">Definisi</th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-app-border font-mono">
                    {data.indexes.map((idx) => (
                      <tr key={idx.name}>
                        <td className="px-4 py-2 font-semibold text-zinc-200">
                          {idx.is_unique && (
                            <span className="mr-1.5 px-1 py-0.5 rounded text-[9px] bg-sem-warning/20 text-sem-warning">
                              UNIQUE
                            </span>
                          )}
                          {idx.name}
                        </td>
                        <td className="px-4 py-2 text-zinc-300">{idx.columns}</td>
                        <td className="px-4 py-2 text-zinc-400">{idx.index_type}</td>
                        <td className="px-4 py-2 text-zinc-400 break-all">{idx.definition}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
