import React from 'react'
import { Link } from 'react-router-dom'

export const ForbiddenPage: React.FC<{ message?: string }> = ({
  message = 'Anda tidak memiliki hak akses untuk tindakan atau database ini.',
}) => {
  return (
    <div className="max-w-xl mx-auto mt-16 bg-app-card border border-sem-danger/30 rounded-2xl p-8 text-center space-y-3">
      <h1 className="text-lg font-bold text-sem-danger">403 · Akses ditolak</h1>
      <p className="text-xs text-zinc-300 leading-relaxed">{message}</p>
      <Link to="/dashboard" className="inline-block mt-2 text-xs font-semibold text-brand-primary hover:underline">
        Kembali ke Database Viewer
      </Link>
    </div>
  )
}
