let csrfToken: string = ''

export function setCSRFToken(token: string) {
  csrfToken = token
}

export function getCSRFToken(): string {
  return csrfToken
}

async function request<T = any>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers || {})

  if (!headers.has('Content-Type') && !(options.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json')
  }

  const method = (options.method || 'GET').toUpperCase()
  if (['POST', 'PUT', 'DELETE', 'PATCH'].includes(method)) {
    if (csrfToken) {
      headers.set('X-CSRF-Token', csrfToken)
    }
  }

  const res = await fetch(endpoint, {
    ...options,
    headers,
    credentials: 'include',
  })

  // Extract new CSRF token if returned in header or response
  const newCSRF = res.headers.get('X-CSRF-Token')
  if (newCSRF) {
    csrfToken = newCSRF
  }

  const contentType = res.headers.get('Content-Type') || ''
  const isJson = contentType.includes('application/json')

  const data = isJson ? await res.json() : await res.text()

  if (isJson && data?.csrf_token) {
    csrfToken = data.csrf_token
  }

  if (!res.ok) {
    const error: any = new Error(data?.error || `Request failed with status ${res.status}`)
    error.status = res.status
    error.data = data
    throw error
  }

  return data as T
}

export const api = {
  // Auth
  getMe: () => request('/api/auth/me'),
  login: (data: any) => request('/api/auth/login', { method: 'POST', body: JSON.stringify(data) }),
  signup: (data: any) => request('/api/auth/signup', { method: 'POST', body: JSON.stringify(data) }),
  logout: () => request('/api/auth/logout', { method: 'POST' }),

  // Viewer
  getViewer: (dbId?: string) => request(`/api/viewer${dbId ? `?db_id=${encodeURIComponent(dbId)}` : ''}`),
  getViewerTable: (dbId: string, schema: string, name: string, page = 1) =>
    request(`/api/viewer/table?db_id=${encodeURIComponent(dbId)}&schema=${encodeURIComponent(schema)}&name=${encodeURIComponent(name)}&page=${page}`),
  queryViewer: (dbId: string, sqlQuery: string) =>
    request('/api/viewer/query', { method: 'POST', body: JSON.stringify({ db_id: dbId, sql_query: sqlQuery }) }),

  // Migration
  getMigrationOverview: (dbId?: string, draft?: string) => {
    const params = new URLSearchParams()
    if (dbId) params.set('db_id', dbId)
    if (draft) params.set('draft', draft)
    const qs = params.toString()
    return request(`/api/migration${qs ? `?${qs}` : ''}`)
  },
  getMigrationDetail: (id: string) => request(`/api/migration/${encodeURIComponent(id)}`),
  createMigrationDraft: (data: {
    db_id: string
    name: string
    sql_script?: string
    sql_up?: string
    sql_down?: string
    is_irreversible?: boolean
    irreversible_reason?: string
    execution_mode?: string
    batch_attribute?: string
    batch_size?: number
  }) => request('/api/migration/drafts', { method: 'POST', body: JSON.stringify(data) }),
  saveMigrationDraft: (
    id: string,
    data: {
      name: string
      sql_script?: string
      sql_up?: string
      sql_down?: string
      is_irreversible?: boolean
      irreversible_reason?: string
      execution_mode?: string
      batch_attribute?: string
      batch_size?: number
    }
  ) => request(`/api/migration/${encodeURIComponent(id)}`, { method: 'POST', body: JSON.stringify(data) }),
  deleteMigrationDraft: (id: string) =>
    request(`/api/migration/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  dryRunMigration: (id: string, data?: any) =>
    request(`/api/migration/${encodeURIComponent(id)}/dry-run`, { method: 'POST', body: JSON.stringify(data || {}) }),
  executeMigration: (id: string, data?: any) =>
    request(`/api/migration/${encodeURIComponent(id)}/execute`, { method: 'POST', body: JSON.stringify(data || {}) }),
  rollbackMigration: (id: string) =>
    request(`/api/migration/${encodeURIComponent(id)}/rollback`, { method: 'POST' }),
  forwardMigration: (id: string) =>
    request(`/api/migration/${encodeURIComponent(id)}/forward`, { method: 'POST' }),
  duplicateMigration: (id: string) =>
    request(`/api/migration/${encodeURIComponent(id)}/duplicate`, { method: 'POST' }),
  submitMigrationForReview: (id: string) =>
    request(`/api/migration/${encodeURIComponent(id)}/submit-review`, { method: 'POST' }),
  approveMigration: (id: string, note?: string) =>
    request(`/api/migration/${encodeURIComponent(id)}/approve`, { method: 'POST', body: JSON.stringify({ note }) }),
  rejectMigration: (id: string, reason: string) =>
    request(`/api/migration/${encodeURIComponent(id)}/reject`, { method: 'POST', body: JSON.stringify({ reason }) }),

  // Admin
  getAdminOverview: () => request('/api/admin/overview'),
  createAdminUser: (data: any) => request('/api/admin/users', { method: 'POST', body: JSON.stringify(data) }),
  setAdminUserRole: (id: string, role: string) =>
    request(`/api/admin/users/${encodeURIComponent(id)}/role`, { method: 'POST', body: JSON.stringify({ role }) }),
  setAdminUserActive: (id: string, active: boolean) =>
    request(`/api/admin/users/${encodeURIComponent(id)}/active`, { method: 'POST', body: JSON.stringify({ active }) }),
  resetAdminUserPassword: (id: string, password: string) =>
    request(`/api/admin/users/${encodeURIComponent(id)}/password`, { method: 'POST', body: JSON.stringify({ password }) }),

  createAdminGroup: (data: { name: string; description: string }) =>
    request('/api/admin/groups', { method: 'POST', body: JSON.stringify(data) }),
  renameAdminGroup: (id: string, data: { name: string; description: string }) =>
    request(`/api/admin/groups/${encodeURIComponent(id)}`, { method: 'POST', body: JSON.stringify(data) }),
  deleteAdminGroup: (id: string) =>
    request(`/api/admin/groups/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  addAdminGroupMember: (groupId: string, userId: string) =>
    request(`/api/admin/groups/${encodeURIComponent(groupId)}/members`, { method: 'POST', body: JSON.stringify({ user_id: userId }) }),
  removeAdminGroupMember: (groupId: string, userId: string) =>
    request(`/api/admin/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}`, { method: 'DELETE' }),
  setAdminGroupLeader: (groupId: string, userId: string) =>
    request(`/api/admin/groups/${encodeURIComponent(groupId)}/leader`, { method: 'POST', body: JSON.stringify({ user_id: userId }) }),

  setAdminGrant: (data: { group_id: string; database_id: string; level: string }) =>
    request('/api/admin/grants', { method: 'POST', body: JSON.stringify(data) }),

  createAdminTarget: (data: any) => request('/api/admin/databases', { method: 'POST', body: JSON.stringify(data) }),
  updateAdminTarget: (id: string, data: any) => request(`/api/admin/databases/${encodeURIComponent(id)}`, { method: 'POST', body: JSON.stringify(data) }),
  testAdminTarget: (id: string) => request(`/api/admin/databases/${encodeURIComponent(id)}/test`, { method: 'POST' }),
  deleteAdminTarget: (id: string) => request(`/api/admin/databases/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  createAdminCode: (data: { group_id: string; code: string }) =>
    request('/api/admin/codes', { method: 'POST', body: JSON.stringify(data) }),
  setAdminCodeActive: (code: string, active: boolean) =>
    request(`/api/admin/codes/${encodeURIComponent(code)}/active`, { method: 'POST', body: JSON.stringify({ active }) }),
}
