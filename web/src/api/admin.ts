import { qs, request } from './client'

export interface User {
  id: string; username: string; displayName: string; role: 'admin' | 'user'
  disabled: boolean; quotaBytes: number; createdAt: string; groupIds: string[]
}
export interface Group { id: string; name: string; parentId?: string; createdAt: string; memberIds: string[] }
export interface Grant {
  id: string; subjectType: 'user' | 'group'; subjectId: string; path: string; perm: 'r' | 'rw'; createdBy: string; createdAt: string
}
export interface AuditEntry { id: number; at: string; userId: string; username: string; action: string; path: string; detail: string; ip: string }
export interface MetricPoint { t: number; v: number; min: number; max: number }
export interface SystemInfo {
  host: { hostname: string; os: string; platform: string; arch: string; cpus: number; memTotal: number; uptimeSec: number
    diskTotal: number; diskUsed: number; diskFree: number; diskFs: string; load1: number }
  app: { version: string; database: string; dataRoot: string }
  users: number
  storage: { indexedBytes: number; indexedFiles: number; topLevel: Record<string, number> }
}

export const adminApi = {
  users: () => request<{ items: User[] }>('GET', '/api/admin/users'),
  createUser: (u: { username: string; displayName: string; password: string; role: string; quotaBytes: number; groupIds: string[] }) =>
    request<User>('POST', '/api/admin/users', u),
  updateUser: (id: string, patch: Partial<Pick<User, 'displayName' | 'role' | 'disabled' | 'quotaBytes' | 'groupIds'>>) =>
    request<User>('PATCH', `/api/admin/users/${id}`, patch),
  deleteUser: (id: string) => request<void>('DELETE', `/api/admin/users/${id}`),
  resetPassword: (id: string, password: string) => request<void>('POST', `/api/admin/users/${id}/password`, { password }),

  groups: () => request<{ items: Group[] }>('GET', '/api/admin/groups'),
  createGroup: (name: string, parentId?: string) => request<Group>('POST', '/api/admin/groups', { name, parentId: parentId ?? '' }),
  updateGroup: (id: string, patch: { name?: string; parentId?: string; memberIds?: string[] }) =>
    request<void>('PATCH', `/api/admin/groups/${id}`, patch),
  deleteGroup: (id: string) => request<void>('DELETE', `/api/admin/groups/${id}`),

  grants: (subjectType?: string, subjectId?: string) =>
    request<{ items: Grant[] }>('GET', `/api/admin/grants${subjectType ? '?' + qs({ subjectType, subjectId: subjectId! }) : ''}`),
  createGrant: (g: { subjectType: string; subjectId: string; path: string; perm: string }) => request<Grant>('POST', '/api/admin/grants', g),
  deleteGrant: (id: string) => request<void>('DELETE', `/api/admin/grants/${id}`),

  audit: (f: { userId?: string; action?: string; path?: string; before?: number }) => {
    const p: Record<string, string> = { limit: '100' }
    for (const [k, v] of Object.entries(f)) if (v) p[k] = String(v)
    return request<{ items: AuditEntry[] }>('GET', `/api/admin/audit?${qs(p)}`)
  },
  metrics: (metrics: string[], range: string) =>
    request<{ from: number; to: number; series: Record<string, MetricPoint[]> }>('GET', `/api/admin/metrics?${qs({ metric: metrics.join(','), range })}`),
  system: () => request<SystemInfo>('GET', '/api/admin/system'),
}
