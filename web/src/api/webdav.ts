import { request } from './client'

export interface DavDir { path: string; alias: string; perm: 'r' | 'rw'; name?: string; missing?: boolean }
export interface DavConfig {
  id: string; name: string; slug: string; username: string; enabled: boolean
  dirs: DavDir[]; createdBy: string; createdAt: string; updatedAt: string
}
export interface DavInput { name: string; slug: string; username: string; password?: string; enabled?: boolean; dirs: DavDir[] }

export const webdavApi = {
  list: () => request<{ items: DavConfig[] }>('GET', '/api/admin/webdav'),
  create: (c: DavInput) => request<{ item: DavConfig; password: string }>('POST', '/api/admin/webdav', c),
  update: (id: string, c: DavInput) => request<DavConfig>('PUT', `/api/admin/webdav/${id}`, c),
  setEnabled: (id: string, enabled: boolean) => request<DavConfig>('PATCH', `/api/admin/webdav/${id}`, { enabled }),
  resetPassword: (id: string) => request<{ password: string }>('POST', `/api/admin/webdav/${id}/password`),
  remove: (id: string) => request<void>('DELETE', `/api/admin/webdav/${id}`),
}

/** The URL clients mount for a config. */
export const davUrl = (slug: string) => `${location.origin}/dav/${slug}/`
