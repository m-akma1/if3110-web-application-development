import React, { useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

export const SignupPage: React.FC = () => {
  const { signup } = useAuth()
  const navigate = useNavigate()

  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [loading, setLoading] = useState(false)

  const [usernameError, setUsernameError] = useState('')
  const [emailError, setEmailError] = useState('')
  const [passwordError, setPasswordError] = useState('')
  const [codeError, setCodeError] = useState('')
  const [generalError, setGeneralError] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setUsernameError('')
    setEmailError('')
    setPasswordError('')
    setCodeError('')
    setGeneralError('')

    let valid = true
    if (!username.trim()) {
      setUsernameError('Username wajib diisi')
      valid = false
    } else if (username.trim().length < 3 || username.trim().length > 32) {
      setUsernameError('3-32 karakter: a-z, 0-9, titik, _ atau -')
      valid = false
    }
    if (!password) {
      setPasswordError('Password wajib diisi')
      valid = false
    } else if (password.length < 8) {
      setPasswordError('Minimal 8 karakter')
      valid = false
    }
    if (!valid) return

    setLoading(true)
    try {
      const res = await signup({
        username: username.trim(),
        email: email.trim(),
        password,
        invite_code: inviteCode.trim(),
      })
      if (res.user) {
        navigate('/dashboard')
      } else {
        navigate('/login')
      }
    } catch (err: any) {
      if (err.data?.username_error) setUsernameError(err.data.username_error)
      if (err.data?.email_error) setEmailError(err.data.email_error)
      if (err.data?.password_error) setPasswordError(err.data.password_error)
      if (err.data?.code_error) setCodeError(err.data.code_error)
      setGeneralError(err.data?.error || 'Gagal mendaftarkan akun, silakan coba lagi.')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex">
      {/* Panel Kiri: Gambar Statik */}
      <div className="hidden lg:flex lg:w-1/2 relative overflow-hidden bg-app-bg border-r border-app-border flex-col justify-between p-12">
        <img
          src="/static/img/auth-bg.svg"
          alt="Database Platform Visual"
          className="absolute inset-0 w-full h-full object-cover pointer-events-none select-none"
        />

        {/* Brand Header */}
        <div className="relative z-10 flex items-center gap-3">
          <div className="w-10 h-10 rounded-xl bg-brand-primary flex items-center justify-center font-bold text-lg text-app-bg shadow-md">
            DB
          </div>
          <span className="text-lg font-bold tracking-tight text-white">DB Platform</span>
        </div>

        {/* Caption Bawah */}
        <div className="relative z-10 max-w-md space-y-3 bg-app-sidebar/90 border border-app-border rounded-2xl p-6">
          <h2 className="text-xl font-bold text-white tracking-tight">
            Kelola seluruh koneksi database dan skema dalam satu tempat.
          </h2>
          <p className="text-xs text-zinc-400 leading-relaxed">
            Pantau tabel, jalankan migrasi skema dengan aman, dan atur kontrol akses berbasis grup secara terpusat.
          </p>
        </div>
      </div>

      {/* Panel Kanan: Container Form Sign Up */}
      <div className="w-full lg:w-1/2 flex items-center justify-center p-6 sm:p-12 bg-app-bg">
        <div
          id="auth-form-container"
          className="w-full max-w-md bg-app-sidebar border border-app-border rounded-2xl p-6 sm:p-8 shadow-2xl"
        >
          <div className="space-y-6">
            {/* Header */}
            <div className="space-y-2.5">
              <div className="text-brand-primary">
                <svg className="w-8 h-8" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M12 3v18M4.2 7.5l15.6 9M19.8 7.5l-15.6 9" />
                </svg>
              </div>
              <h1 className="text-2xl font-bold text-white tracking-tight">Create an account</h1>
              <p className="text-xs sm:text-sm text-zinc-400 leading-relaxed">
                Daftar dengan username Anda. Kode tim bersifat opsional. Tanpa kode, Anda tetap dapat masuk dan Platform Admin dapat memberi akses nanti.
              </p>
            </div>

            {generalError && (
              <div className="p-3 rounded-lg bg-sem-danger/10 border border-sem-danger/30 text-xs font-medium text-sem-danger">
                {generalError}
              </div>
            )}

            {/* Form Sign Up */}
            <form onSubmit={handleSubmit} className="space-y-4">
              {/* Input Username */}
              <div className="space-y-1.5">
                <div className="flex items-center justify-between gap-2">
                  <label htmlFor="signup-username" className="block text-xs font-semibold text-zinc-200">
                    Username
                  </label>
                  {usernameError && (
                    <span className="text-xs font-medium text-sem-danger flex items-center gap-1">
                      <svg className="w-3.5 h-3.5 shrink-0" viewBox="0 0 20 20" fill="currentColor">
                        <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-5a.75.75 0 01.75.75v4.5a.75.75 0 01-1.5 0v-4.5A.75.75 0 0110 5zm0 10a1 1 0 100-2 1 1 0 000 2z" clipRule="evenodd" />
                      </svg>
                      {usernameError}
                    </span>
                  )}
                </div>
                <input
                  type="text"
                  id="signup-username"
                  name="username"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder="contoh: budi.santoso"
                  autoComplete="username"
                  autoCapitalize="none"
                  spellCheck="false"
                  minLength={3}
                  maxLength={32}
                  required
                  autoFocus
                  className={`w-full px-3.5 py-2.5 text-sm rounded-lg border bg-app-input text-white placeholder:text-zinc-500 focus:outline-none focus:ring-2 transition ${
                    usernameError
                      ? 'border-sem-danger focus:border-sem-danger focus:ring-sem-danger/30'
                      : 'border-app-border focus:border-brand-primary focus:ring-brand-primary/50'
                  }`}
                />
              </div>

              {/* Input Email */}
              <div className="space-y-1.5">
                <div className="flex items-center justify-between gap-2">
                  <label htmlFor="signup-email" className="block text-xs font-semibold text-zinc-200">
                    Email <span className="font-normal text-zinc-500">(opsional)</span>
                  </label>
                  {emailError && (
                    <span className="text-xs font-medium text-sem-danger flex items-center gap-1">
                      <svg className="w-3.5 h-3.5 shrink-0" viewBox="0 0 20 20" fill="currentColor">
                        <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-5a.75.75 0 01.75.75v4.5a.75.75 0 01-1.5 0v-4.5A.75.75 0 0110 5zm0 10a1 1 0 100-2 1 1 0 000 2z" clipRule="evenodd" />
                      </svg>
                      {emailError}
                    </span>
                  )}
                </div>
                <input
                  type="email"
                  id="signup-email"
                  name="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="nama@email.com"
                  autoComplete="email"
                  className={`w-full px-3.5 py-2.5 text-sm rounded-lg border bg-app-input text-white placeholder:text-zinc-500 focus:outline-none focus:ring-2 transition ${
                    emailError
                      ? 'border-sem-danger focus:border-sem-danger focus:ring-sem-danger/30'
                      : 'border-app-border focus:border-brand-primary focus:ring-brand-primary/50'
                  }`}
                />
              </div>

              {/* Input Password */}
              <div className="space-y-1.5">
                <div className="flex items-center justify-between gap-2">
                  <label htmlFor="signup-password" className="block text-xs font-semibold text-zinc-200">
                    Password
                  </label>
                  {passwordError && (
                    <span className="text-xs font-medium text-sem-danger flex items-center gap-1">
                      <svg className="w-3.5 h-3.5 shrink-0" viewBox="0 0 20 20" fill="currentColor">
                        <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-5a.75.75 0 01.75.75v4.5a.75.75 0 01-1.5 0v-4.5A.75.75 0 0110 5zm0 10a1 1 0 100-2 1 1 0 000 2z" clipRule="evenodd" />
                      </svg>
                      {passwordError}
                    </span>
                  )}
                </div>
                <div className="relative">
                  <input
                    type={showPassword ? 'text' : 'password'}
                    id="signup-password"
                    name="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="Minimal 8 karakter"
                    autoComplete="new-password"
                    required
                    className={`w-full pl-3.5 pr-10 py-2.5 text-sm rounded-lg border bg-app-input text-white placeholder:text-zinc-500 focus:outline-none focus:ring-2 transition ${
                      passwordError
                        ? 'border-sem-danger focus:border-sem-danger focus:ring-sem-danger/30'
                        : 'border-app-border focus:border-brand-primary focus:ring-brand-primary/50'
                    }`}
                  />
                  <button
                    type="button"
                    onClick={() => setShowPassword(!showPassword)}
                    tabIndex={-1}
                    className="absolute inset-y-0 right-0 pr-3 flex items-center text-zinc-400 hover:text-zinc-200 focus:outline-none cursor-pointer"
                  >
                    {showPassword ? (
                      <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M13.875 18.825A10.05 10.05 0 0112 19c-4.478 0-8.268-2.943-9.543-7a9.97 9.97 0 011.563-3.029m5.858.908a3 3 0 114.243 4.243M9.878 9.878l4.242 4.242M9.88 9.88l-3.29-3.29m7.532 7.532l3.29 3.29M3 3l3.59 3.59m0 0A9.953 9.953 0 0112 5c4.478 0 8.268 2.943 9.543 7a10.025 10.025 0 01-4.132 5.411m0 0L21 21" />
                      </svg>
                    ) : (
                      <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M2.458 12C3.732 7.943 7.523 5 12 5c4.478 0 8.268 2.943 9.542 7-1.274 4.057-5.064 7-9.542 7-4.477 0-8.268-2.943-9.542-7z" />
                      </svg>
                    )}
                  </button>
                </div>
              </div>

              {/* Input Invite Code */}
              <div className="space-y-1.5">
                <div className="flex items-center justify-between gap-2">
                  <label htmlFor="signup-invite-code" className="block text-xs font-semibold text-zinc-200">
                    Team Invite Code <span className="font-normal text-zinc-500">(opsional)</span>
                  </label>
                  {codeError && (
                    <span className="text-xs font-medium text-sem-danger flex items-center gap-1">
                      <svg className="w-3.5 h-3.5 shrink-0" viewBox="0 0 20 20" fill="currentColor">
                        <path fillRule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-5a.75.75 0 01.75.75v4.5a.75.75 0 01-1.5 0v-4.5A.75.75 0 0110 5zm0 10a1 1 0 100-2 1 1 0 000 2z" clipRule="evenodd" />
                      </svg>
                      {codeError}
                    </span>
                  )}
                </div>
                <input
                  type="text"
                  id="signup-invite-code"
                  name="invite_code"
                  value={inviteCode}
                  onChange={(e) => setInviteCode(e.target.value)}
                  placeholder="Kode Tim dari Platform Admin"
                  className={`w-full px-3.5 py-2.5 text-sm font-mono rounded-lg border bg-app-input text-white placeholder:text-zinc-500 focus:outline-none focus:ring-2 transition ${
                    codeError
                      ? 'border-sem-danger focus:border-sem-danger focus:ring-sem-danger/30'
                      : 'border-app-border focus:border-brand-primary focus:ring-brand-primary/50'
                  }`}
                />
              </div>

              {/* Tombol Submit */}
              <div className="pt-1">
                <button
                  type="submit"
                  disabled={loading}
                  className="w-full py-2.5 px-4 bg-brand-primary hover:bg-brand-primary-hover text-app-bg text-xs sm:text-sm font-semibold rounded-lg shadow-lg shadow-brand-primary/20 transition cursor-pointer disabled:opacity-50"
                >
                  {loading ? 'Memproses...' : 'Create Account'}
                </button>
              </div>
            </form>

            {/* Footer Link ke Login */}
            <p className="text-center text-xs text-zinc-400 pt-1">
              Already have an account?
              <Link to="/login" className="font-semibold text-brand-primary hover:text-brand-primary-hover hover:underline ml-1">
                Log in
              </Link>
            </p>
          </div>
        </div>
      </div>
    </div>
  )
}
