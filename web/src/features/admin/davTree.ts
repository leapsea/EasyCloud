import type { DavDir } from '@/api/webdav'

// What a WebDAV client sees at the root, in order: existing data shared
// directly, or a folder (only a name) gathering existing data, read-only.
// Stored as rows; rows of a folder carry its name.
export interface Leaf { path: string; alias: string; isDir?: boolean }
export type DataNode = Leaf & { kind: 'data'; perm: 'r' | 'rw' }
export type FolderNode = { kind: 'folder'; name: string; items: Leaf[] }
export type RootNode = DataNode | FolderNode

export function toTree(dirs: DavDir[]): RootNode[] {
  const out: RootNode[] = []
  for (const d of dirs) {
    if (!d.folder) { out.push({ kind: 'data', path: d.path, alias: d.alias, perm: d.perm, isDir: d.isDir }); continue }
    let f = out.find((n): n is FolderNode => n.kind === 'folder' && n.name === d.folder)
    if (!f) { f = { kind: 'folder', name: d.folder, items: [] }; out.push(f) }
    f.items.push({ path: d.path, alias: d.alias, isDir: d.isDir })
  }
  return out
}

export const baseName = (path: string) => (path === '/' ? 'data' : path.split('/').filter(Boolean).pop() ?? '')
export const leafName = (l: Leaf) => l.alias.trim() || baseName(l.path)
// A picked path may be a file; until the server reports it, a name with an extension counts as one.
export const looksDir = (l: Leaf) => l.isDir ?? !/\.[^/]+$/.test(l.path)
