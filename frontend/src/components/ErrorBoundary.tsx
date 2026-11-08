import React, { Component, ErrorInfo, ReactNode } from 'react'

interface Props {
  children: ReactNode
}

interface State {
  hasError: boolean
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  public state: State = {
    hasError: false,
    error: null,
  }

  public static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  public componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('Uncaught error:', error, errorInfo)
  }

  public render() {
    if (this.state.hasError) {
      return (
        <div className="min-h-screen bg-app-bg text-zinc-100 flex items-center justify-center p-6">
          <div className="max-w-lg w-full bg-app-card border border-sem-danger/40 rounded-2xl p-6 shadow-xl">
            <h2 className="text-base font-bold text-sem-danger mb-2">Terjadi Kesalahan Aplikasi</h2>
            <p className="text-xs text-zinc-400 mb-4">
              {this.state.error?.message || 'Terjadi kesalahan tidak terduga saat menampilkan halaman.'}
            </p>
            <div className="flex gap-3">
              <button
                onClick={() => window.location.reload()}
                className="px-4 py-2 bg-brand-primary text-app-bg text-xs font-bold rounded-xl hover:bg-brand-primary-hover transition"
              >
                Muat Ulang Halaman
              </button>
              <button
                onClick={() => (window.location.href = '/dashboard')}
                className="px-4 py-2 bg-app-card-hover border border-app-border text-zinc-200 text-xs font-semibold rounded-xl hover:bg-app-active transition"
              >
                Kembali ke Dashboard
              </button>
            </div>
          </div>
        </div>
      )
    }

    return this.props.children
  }
}
