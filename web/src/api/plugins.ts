import { request } from './client'

export interface ViewerPlugin { id: string; version: string; label: string; entry: string; mimes: string[]; extensions: string[]; priority: number }
export interface ConfigField { key: string; label: string; type: 'string' | 'secret' | 'number' | 'bool' | 'url'; required?: boolean; default?: unknown; placeholder?: string; help?: string }
export interface Manifest {
  id: string; name: string; version: string; type: 'viewer' | 'backend' | 'connector'; description: string; author: string
  permissions: string[]; config?: ConfigField[]; engines?: Record<string, string>
}
export interface InstalledPlugin {
  manifest: Manifest; enabled: boolean; config: Record<string, unknown>; signedBy: string
  installedAt: string; updatedAt: string; status: 'running' | 'stopped' | 'error' | 'ready'; error?: string
}
export interface RepoPlugin { id: string; name: string; type: string; description: string; latest?: string; installed?: string; update?: boolean }

export const pluginsApi = {
  viewers: () => request<{ items: ViewerPlugin[]; office: boolean }>('GET', '/api/plugins/viewers'),
  list: () => request<{ items: InstalledPlugin[]; repository: string }>('GET', '/api/admin/plugins'),
  async upload(file: File): Promise<InstalledPlugin> {
    const res = await fetch('/api/admin/plugins', { method: 'POST', body: file, headers: { 'Content-Type': 'application/zip' } })
    if (!res.ok) throw await (await import('./client')).parseError(res)
    return res.json()
  },
  repository: () => request<{ items: RepoPlugin[] }>('GET', '/api/admin/plugins/repository'),
  installFromRepo: (id: string, version?: string) => request<InstalledPlugin>('POST', '/api/admin/plugins/repository/install', { id, version: version ?? '' }),
  update: (id: string, patch: { enabled?: boolean; config?: Record<string, unknown> }) => request<void>('PATCH', `/api/admin/plugins/${id}`, patch),
  remove: (id: string) => request<void>('DELETE', `/api/admin/plugins/${id}`),
}

export const fileToken = (path: string) => request<{ url: string; expiresIn: number }>('POST', '/api/files/token', { path })
