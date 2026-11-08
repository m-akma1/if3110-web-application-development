import React from 'react'

export const EmptyState: React.FC<{ title: string; message: string }> = ({ title, message }) => {
  return (
    <div className="max-w-xl mx-auto my-10 bg-app-card border border-app-border rounded-2xl p-8 text-center space-y-3">
      <div className="text-zinc-500 flex justify-center">
        <svg className="w-10 h-10" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5" d="M3 7.5l9-4.5 9 4.5M3 7.5v9l9 4.5m-9-13.5l9 4.5m0 9v-9m0 9l9-4.5v-9m-9 4.5l9-4.5" />
        </svg>
      </div>
      <h2 className="text-lg font-bold text-white">{title}</h2>
      <p className="text-xs text-zinc-400 leading-relaxed">{message}</p>
    </div>
  )
}
