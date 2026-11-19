import { qs, request } from './client'

export interface FileEntry { name: string; path: string; isDir: boolean; size: number; modTime: string; mime?: string; width?: number; height?: number; thumb?: boolean; protected?: boolean }
export type Access = 'rw' | 'r' | 'nav'
export interface MediaInfo {
  source: 'ffprobe' | 'builtin'; container?: string; duration?: number; bitrate?: number
  video?: { codec?: string; profile?: string; width?: number; height?: number; fps?: number; bitrate?: number; pixFmt?: string }
  audio?: { codec?: string; sampleRate?: number; channels?: number; bitrate?: number }
}
export interface FileDetails extends FileEntry { createdAt?: string; media?: MediaInfo; image?: { width: number; height: number; format?: string } }
export interface Listing { path: string; access: Access; entries: FileEntry[] }
export interface SearchHit extends FileEntry { width?: number; height?: number }
export interface TrashItem { id: string; originPath: string; isDir: boolean; size: number; deletedAt: string; expireAt: string }
export type ConflictPolicy = 'fail' | 'rename' | 'overwrite'

export const filesApi = {
  list: (path: string) => request<Listing>('GET', `/api/files/list?${qs({ path })}`),
  mkdir: (path: string) => request<FileEntry>('POST', '/api/files/mkdir', { path }),
  rename: (path: string, name: string) => request<FileEntry>('POST', '/api/files/rename', { path, name }),
  move: (sources: string[], dest: string, onConflict: ConflictPolicy = 'fail') =>
    request<{ paths: string[] }>('POST', '/api/files/move', { sources, dest, onConflict }),
  copy: (sources: string[], dest: string, onConflict: ConflictPolicy = 'rename') =>
    request<{ paths: string[] }>('POST', '/api/files/copy', { sources, dest, onConflict }),
  trash: (paths: string[]) => request<{ items: TrashItem[] }>('POST', '/api/files/trash', { paths }),
  search: (q: string, path?: string) =>
    request<{ items: SearchHit[]; truncated: boolean }>('GET', `/api/files/search?${qs(path ? { q, path } : { q })}`),
  usage: (path: string) => request<{ bytes: number; files: number }>('GET', `/api/files/usage?${qs({ path })}`),
  // Player: whether a video is streamed with a playback fix (see internal/media/mp4fix).
  playback: (path: string) => request<{ fixed: boolean; reasons: string[] }>('GET', `/api/files/playback?${qs({ path })}`),
  details: (path: string) => request<FileDetails>('GET', `/api/files/details?${qs({ path })}`),
  streamUrl: (path: string) => `/api/files/stream?${qs({ path })}`,
  downloadUrl: (path: string, inline = false) =>
    `/api/files/download?${qs(inline ? { path, inline: '1' } : { path })}`,
}

export const trashApi = {
  list: () => request<{ items: TrashItem[] }>('GET', '/api/trash'),
  restore: (id: string, onConflict: ConflictPolicy = 'rename') =>
    request<{ path: string }>('POST', `/api/trash/${encodeURIComponent(id)}/restore`, { onConflict }),
  purge: (id: string) => request<void>('DELETE', `/api/trash/${encodeURIComponent(id)}`),
  empty: () => request<{ purged: number }>('DELETE', '/api/trash'),
}

export interface SettingItem { key: string; value: number; default: number; override: boolean }
export const settingsApi = {
  list: () => request<{ items: SettingItem[] }>('GET', '/api/settings'),
  set: (key: string, value: number) => request<void>('PUT', `/api/settings/${encodeURIComponent(key)}`, { value }),
  reset: (key: string) => request<void>('DELETE', `/api/settings/${encodeURIComponent(key)}`),
  clientConfig: () => request<{ uploadChunkSize: number; uploadMaxFileSize: number }>('GET', '/api/client-config'),
}

export interface Me { id: string; username: string; displayName: string; isAdmin: boolean; home: string; quotaBytes: number }
export const authApi = {
  me: () => request<Me>('GET', '/api/auth/me'),
  login: (username: string, password: string) => request<Me>('POST', '/api/auth/login', { username, password }),
  logout: () => request<void>('POST', '/api/auth/logout'),
  changePassword: (current: string, next: string) => request<void>('POST', '/api/auth/password', { current, next }),
}
