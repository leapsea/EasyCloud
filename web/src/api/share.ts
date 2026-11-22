import { request, qs } from './client'
import type { FileEntry } from './files'
import type { ViewerPlugin } from './plugins'

export interface Share {
  id: string; ownerId: string; path: string; isDir: boolean; hasPassword: boolean
  expireAt?: string; maxDownloads: number; downloads: number; createdAt: string
}
export interface ShareMeta {
  id: string; name: string; isDir: boolean; size?: number; mime?: string; owner: string
  expireAt?: string; hasPassword: boolean; unlocked: boolean; downloadsLeft: number
  // Online preview is offered only for shares without a download limit.
  preview: boolean; viewers?: ViewerPlugin[]
}
export interface AppPassword { id: string; name: string; createdAt: string; lastUsedAt?: string }

export const shareApi = {
  create: (s: { path: string; password: string; expireDays: number; maxDownloads: number }) => request<Share & { url: string }>('POST', '/api/shares', s),
  mine: () => request<{ items: Share[] }>('GET', '/api/shares'),
  all: () => request<{ items: Share[] }>('GET', '/api/admin/shares'),
  remove: (id: string) => request<void>('DELETE', `/api/shares/${id}`),
  // Public visitor API
  meta: (id: string) => request<ShareMeta>('GET', `/api/s/${id}/`),
  unlock: (id: string, password: string) => request<{ token: string }>('POST', `/api/s/${id}/unlock`, { password }),
  list: (id: string, path: string) => request<{ path: string; entries: FileEntry[] }>('GET', `/api/s/${id}/list?${qs({ path })}`),
  downloadUrl: (id: string, path: string, token?: string) => `/api/s/${id}/download?${qs(token ? { path, token } : { path })}`,
  zipUrl: (id: string, path: string, token?: string) => `/api/s/${id}/zip?${qs(token ? { path, token } : { path })}`,
  previewUrl: (id: string, path: string, token?: string) => `/api/s/${id}/download?${qs(token ? { path, token, inline: '1' } : { path, inline: '1' })}`,
  previewToken: (id: string, path: string) => request<{ url: string; expiresIn: number }>('POST', `/api/s/${id}/token`, { path }),
  thumbUrl: (id: string, path: string, size = 256) => `/api/s/${id}/thumb?${qs({ path, size: String(size) })}`,
}

export const appPasswordApi = {
  list: () => request<{ items: AppPassword[] }>('GET', '/api/auth/app-passwords'),
  create: (name: string) => request<{ item: AppPassword; password: string }>('POST', '/api/auth/app-passwords', { name }),
  remove: (id: string) => request<void>('DELETE', `/api/auth/app-passwords/${id}`),
}
