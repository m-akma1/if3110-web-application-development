import React, { createContext, useContext, useState, useEffect } from 'react'
import { api, setCSRFToken } from '../api/client'
import { User, GroupSummary, DatabaseAccessSummary, AuthContextType } from '../types'

const AuthContext = createContext<AuthContextType | undefined>(undefined)

export const AuthProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [user, setUser] = useState<User | null>(null)
  const [groups, setGroups] = useState<GroupSummary[]>([])
  const [databases, setDatabases] = useState<DatabaseAccessSummary[]>([])
  const [activeDBID, setActiveDBID] = useState<string>('')
  const [csrfToken, setCsrfTokenState] = useState<string>('')
  const [loading, setLoading] = useState<boolean>(true)

  const refreshAuth = async () => {
    try {
      const data = await api.getMe()
      setUser(data.user)
      setGroups(data.groups || [])
      setDatabases(data.databases || [])
      if (data.csrf_token) {
        setCSRFToken(data.csrf_token)
        setCsrfTokenState(data.csrf_token)
      }
      if (!activeDBID && data.databases && data.databases.length > 0) {
        setActiveDBID(data.databases[0].database_id)
      }
    } catch (err) {
      setUser(null)
      setGroups([])
      setDatabases([])
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    refreshAuth()
  }, [])

  const login = async (credentials: any) => {
    const data = await api.login(credentials)
    setUser(data.user)
    setGroups(data.groups || [])
    setDatabases(data.databases || [])
    if (data.csrf_token) {
      setCSRFToken(data.csrf_token)
      setCsrfTokenState(data.csrf_token)
    }
    if (data.databases && data.databases.length > 0) {
      setActiveDBID(data.databases[0].database_id)
    }
    return data
  }

  const signup = async (data: any) => {
    const res = await api.signup(data)
    if (res.user) {
      setUser(res.user)
      setGroups(res.groups || [])
      setDatabases(res.databases || [])
      if (res.databases && res.databases.length > 0) {
        setActiveDBID(res.databases[0].database_id)
      }
    }
    if (res.csrf_token) {
      setCSRFToken(res.csrf_token)
      setCsrfTokenState(res.csrf_token)
    }
    return res
  }

  const logout = async () => {
    try {
      await api.logout()
    } finally {
      setUser(null)
      setGroups([])
      setDatabases([])
      setActiveDBID('')
    }
  }

  return (
    <AuthContext.Provider
      value={{
        user,
        groups,
        databases,
        activeDBID,
        setActiveDBID,
        loading,
        csrfToken,
        login,
        signup,
        logout,
        refreshAuth,
      }}
    >
      {children}
    </AuthContext.Provider>
  )
}

export const useAuth = (): AuthContextType => {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider')
  }
  return context
}
