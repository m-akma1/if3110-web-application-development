import React from 'react'

export const ErrorState: React.FC<{
  title: string
  message: string
  onRetry?: () => void
}> = ({ title, message, onRetry }) => {
  return (
    <div className="max-w-xl mx-auto my-10 bg-app-card border border-sem-danger/30 rounded-2xl p-8 text-center space-y-3" role="alert">
      <div className="text-sem-danger flex justify-center">
        <svg className="w-10 h-10" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="1.5" d="M12 9v3.75m0 3.75h.008M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" />
        </svg>
      </div>
      <h2 className="text-lg font-bold text-white">{title}</h2>
      <p className="text-xs text-zinc-300 leading-relaxed break-words font-mono">{message}</p>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="mt-2 px-4 py-2 rounded-lg bg-brand-primary hover:bg-brand-primary-hover text-app-bg text-xs font-semibold transition cursor-pointer"
        >
          Coba lagi
        </button>
      )}
    </div>
  )
}
