import React from 'react'
import { GroupSummary } from '../types'

export const NoDatabaseAccess: React.FC<{ groups?: GroupSummary[] }> = ({ groups = [] }) => {
  return (
    <div className="max-w-xl mx-auto mt-16 bg-app-card border border-app-border rounded-2xl p-8 text-center space-y-3">
      <div className="text-sem-warning flex justify-center">
        <svg className="w-10 h-10" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5" d="M4 7v10c0 2.21 3.582 4 8 4s8-1.79 8-4V7M4 7c0 2.21 3.582 4 8 4s8-1.79 8-4M4 7c0-2.21 3.582-4 8-4s8 1.79 8 4" />
        </svg>
      </div>
      <h2 className="text-lg font-bold text-white">Belum ada database yang dapat diakses</h2>
      <p className="text-xs text-zinc-400 leading-relaxed">
        Akses database diberikan melalui group. Minta Platform Admin menambahkan Anda ke group yang memiliki akses,
        atau memberi akses pada group yang sudah Anda ikuti.
      </p>
      {groups && groups.length > 0 && (
        <div className="flex flex-wrap justify-center gap-1.5 pt-2">
          <span className="text-[10px] text-zinc-500 uppercase tracking-wider mr-1 self-center">Group Anda:</span>
          {groups.map((g) => (
            <span key={g.id} className="px-2 py-0.5 rounded-md text-[10px] font-mono bg-app-input text-zinc-300 border border-app-border">
              {g.name}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}
