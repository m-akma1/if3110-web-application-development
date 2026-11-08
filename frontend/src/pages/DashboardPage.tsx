import React, { useState, useEffect } from 'react'
import { useAuth } from '../context/AuthContext'
import { api } from '../api/client'
import { TableSummary, TableDataResult } from '../types'
import { DataGrid } from '../components/DataGrid'
import { EmptyState } from '../components/EmptyState'
import { ErrorState } from '../components/ErrorState'
import { NoDatabaseAccess } from '../components/NoDatabaseAccess'

interface TableGroup {
  schema: string
  tables: TableSummary[]
}

function groupBySchema(tables: TableSummary[]): TableGroup[] {
  const out: TableGroup[] = []
  for (const t of tables) {
    if (out.length === 0 || out[out.length - 1].schema !== t.schema) {
      out.push({ schema: t.schema, tables: [] })
    }
    out[out.length - 1].tables.push(t)
  }
  return out
}

function sqlIdent(s: string): string {
  if (/^[a-z_][a-z0-9_]*$/i.test(s)) {
    return s
  }
  return `"${s.replace(/"/g, '""')}"`
}

function starterQuery(tables: TableSummary[]): string {
  if (!tables || tables.length === 0) return ''
  const t = tables[0]
  let name = sqlIdent(t.name)
  if (t.schema !== 'public') {
    name = `${sqlIdent(t.schema)}.${name}`
  }
  return `SELECT * FROM ${name} LIMIT 25;`
}

export const DashboardPage: React.FC = () => {
  const { user, groups, databases, activeDBID } = useAuth()

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [tables, setTables] = useState<TableSummary[]>([])
  const [selectedTableKey, setSelectedTableKey] = useState<string>('')
  const [dataResult, setDataResult] = useState<TableDataResult | null>(null)
  const [sqlQuery, setSqlQuery] = useState<string>('')
  const [queryLoading, setQueryLoading] = useState(false)

  const activeDB = databases.find((d) => d.database_id === activeDBID)
  const dbLabel = activeDB ? activeDB.database_name : activeDBID

  const forbiddenKeyword = (() => {
    const m = sqlQuery.match(
      /\b(INSERT|UPDATE|DELETE|DROP|ALTER|TRUNCATE|CREATE|REPLACE|GRANT|REVOKE|MERGE|CALL|EXEC|EXECUTE|COPY|VACUUM)\b/i
    )
    return m ? m[1].toUpperCase() : ''
  })()

  const hasMultiStatement = (() => {
    const trimmed = sqlQuery.trim().replace(/;$/, '')
    return trimmed.includes(';')
  })()

  const fetchViewerData = async (dbId: string) => {
    setLoading(true)
    setError(null)
    try {
      const res = await api.getViewer(dbId)
      if (res.error) {
        setError(res.error)
        setTables([])
        setDataResult(null)
      } else {
        const tbls: TableSummary[] = res.tables || []
        setTables(tbls)
        if (res.selected) {
          setDataResult(res.selected)
          setSelectedTableKey(`${res.selected.schema_name}.${res.selected.table_name}`)
        } else if (tbls.length > 0) {
          setSelectedTableKey(`${tbls[0].schema}.${tbls[0].name}`)
        }
        setSqlQuery(starterQuery(tbls))
      }
    } catch (err: any) {
      setError(err.message || 'Gagal memuat database')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (activeDBID) {
      fetchViewerData(activeDBID)
    } else {
      setLoading(false)
    }
  }, [activeDBID])

  const handleSelectTable = async (t: TableSummary) => {
    const key = `${t.schema}.${t.name}`
    setSelectedTableKey(key)
    try {
      const res = await api.getViewerTable(activeDBID, t.schema, t.name, 1)
      setDataResult(res)
    } catch (err: any) {
      setDataResult({
        database_id: activeDBID,
        schema_name: t.schema,
        table_name: t.name,
        columns: [],
        rows: [],
        total_rows: 0,
        current_page: 1,
        total_pages: 1,
        query_error: err.message || 'Gagal memuat tabel',
      })
    }
  }

  const handlePageChange = async (newPage: number) => {
    if (!dataResult) return
    try {
      const res = await api.getViewerTable(activeDBID, dataResult.schema_name, dataResult.table_name, newPage)
      setDataResult(res)
    } catch (err: any) {
      // error handled in dataResult
    }
  }

  const handleExecuteQuery = async (e: React.FormEvent) => {
    e.preventDefault()
    if (forbiddenKeyword || hasMultiStatement || !sqlQuery.trim() || queryLoading) {
      return
    }

    setQueryLoading(true)
    try {
      const res = await api.queryViewer(activeDBID, sqlQuery)
      setDataResult(res)
    } catch (err: any) {
      setDataResult({
        database_id: activeDBID,
        schema_name: '',
        table_name: '',
        columns: [],
        rows: [],
        total_rows: 0,
        current_page: 1,
        total_pages: 1,
        is_query_result: true,
        query_error: err.message || 'Eksekusi query gagal',
      })
    } finally {
      setQueryLoading(false)
    }
  }

  if (!activeDBID) {
    return <NoDatabaseAccess groups={groups} />
  }

  if (error) {
    return (
      <ErrorState
        title="Database tidak tersedia"
        message={error}
        onRetry={() => fetchViewerData(activeDBID)}
      />
    )
  }

  return (
    <div className="flex flex-col md:flex-row gap-6 h-full">
      {/* Sidebar Daftar Tabel */}
      <div className="w-full md:w-60 bg-app-card border border-app-border rounded-2xl p-4 flex flex-col shrink-0 max-h-60 md:max-h-none">
        <h3 className="text-xs font-bold text-zinc-400 uppercase tracking-wider mb-3 px-2">
          Daftar Tabel
        </h3>
        <div className="space-y-3 overflow-y-auto flex-1">
          {tables.length === 0 ? (
            <p className="px-2 text-[11px] text-zinc-500">Belum ada tabel.</p>
          ) : (
            groupBySchema(tables).map((grp) => (
              <div key={grp.schema} className="space-y-1">
                <h4 className="px-2 text-[10px] font-semibold uppercase tracking-wider text-zinc-500 font-mono">
                  {grp.schema}
                </h4>
                {grp.tables.map((tbl) => {
                  const key = `${tbl.schema}.${tbl.name}`
                  const isSelected = selectedTableKey === key
                  return (
                    <button
                      key={key}
                      type="button"
                      onClick={() => handleSelectTable(tbl)}
                      className={`w-full text-left px-3 py-2 rounded-xl text-xs transition flex justify-between items-center cursor-pointer ${
                        isSelected
                          ? 'bg-brand-primary/15 text-brand-primary border border-brand-primary/30 font-bold'
                          : 'text-zinc-300 hover:bg-app-active border border-transparent'
                      }`}
                    >
                      <span className="truncate font-mono text-xs">{tbl.name}</span>
                    </button>
                  )
                })}
              </div>
            ))
          )}
        </div>
      </div>

      {/* Main Panel: Read-Only Query Runner + Data Grid */}
      <div className="flex-1 min-w-0 space-y-5">
        {/* Read-Only SQL Query Runner */}
        <div className="bg-app-card border border-app-border rounded-2xl p-4 shadow-xl space-y-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2">
              <span className="text-xs font-bold text-white uppercase tracking-wider">
                SQL Query Explorer
              </span>
              <span className="px-2 py-0.5 rounded text-[10px] font-semibold bg-brand-primary/10 text-brand-primary border border-brand-primary/20">
                SELECT Only (Read-Only Guard)
              </span>
              <span className="px-2 py-0.5 rounded text-[10px] font-mono bg-app-input text-zinc-300 border border-app-border">
                Target DB: {dbLabel}
              </span>
            </div>
            <span className="text-xs text-zinc-400">
              Perubahan data/skema wajib melalui menu Migration
            </span>
          </div>

          <form onSubmit={handleExecuteQuery} className="space-y-2.5">
            <div className="space-y-2">
              <textarea
                name="sql_query"
                rows={3}
                value={sqlQuery}
                onChange={(e) => setSqlQuery(e.target.value)}
                placeholder="SELECT * FROM nama_tabel LIMIT 25;"
                className={`w-full px-3.5 py-2.5 text-xs font-mono rounded-lg border bg-app-input text-white placeholder:text-zinc-500 focus:outline-none focus:ring-2 transition resize-y ${
                  forbiddenKeyword !== '' || hasMultiStatement
                    ? 'border-sem-danger focus:border-sem-danger focus:ring-sem-danger/30'
                    : 'border-app-border focus:border-brand-primary focus:ring-brand-primary/50'
                }`}
              ></textarea>
              <div className="flex justify-end">
                <button
                  type="submit"
                  disabled={
                    forbiddenKeyword !== '' ||
                    hasMultiStatement ||
                    sqlQuery.trim() === '' ||
                    queryLoading
                  }
                  className="px-4 py-2 rounded-lg bg-brand-primary hover:bg-brand-primary-hover disabled:opacity-40 disabled:cursor-not-allowed text-app-bg text-xs font-semibold transition cursor-pointer shrink-0"
                >
                  {queryLoading ? 'Menjalankan...' : 'Jalankan Query'}
                </button>
              </div>
            </div>

            {/* Peringatan Real-Time */}
            {forbiddenKeyword !== '' && (
              <div className="p-2.5 rounded-lg bg-sem-danger/10 border border-sem-danger/30 text-xs text-sem-danger flex items-center gap-2">
                <svg className="w-4 h-4 shrink-0" viewBox="0 0 20 20" fill="currentColor">
                  <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-5a.75.75 0 01.75.75v4.5a.75.75 0 01-1.5 0v-4.5A.75.75 0 0110 5zm0 10a1 1 0 100-2 1 1 0 000 2z" clipRule="evenodd" />
                </svg>
                <span>
                  Perintah <strong className="font-mono underline">{forbiddenKeyword}</strong> ditolak! Halaman utama hanya mengizinkan pembacaan data (<code className="font-mono">SELECT</code>).
                </span>
              </div>
            )}

            {forbiddenKeyword === '' && hasMultiStatement && (
              <div className="p-2.5 rounded-lg bg-sem-warning/10 border border-sem-warning/20 text-xs text-sem-warning flex items-center gap-2">
                <span>
                  Multi-statement (<code className="font-mono">;</code>) tidak diizinkan pada Data Viewer.
                </span>
              </div>
            )}
          </form>
        </div>

        {/* Data Grid or Empty State */}
        {dataResult ? (
          <DataGrid data={dataResult} onPageChange={handlePageChange} />
        ) : tables.length === 0 ? (
          <EmptyState
            title="Database ini belum memiliki tabel"
            message="Buat tabel melalui menu Migration, atau jalankan query SELECT di atas."
          />
        ) : null}
      </div>
    </div>
  )
}
