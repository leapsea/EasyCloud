import type { ViewerPlugin } from '@/api/plugins'

// How a shared file can be opened online, in the same order as the file
// manager: a viewer plugin first, then the built-in image / video / audio /
// PDF and text previews. null: no online preview (e.g. Office files, or a
// type whose plugin is not installed) — the visitor downloads it.
export type PreviewKind = 'plugin' | 'image' | 'video' | 'audio' | 'frame'

const IMAGE = /^image\/(jpeg|png|gif|webp|svg\+xml|avif|bmp)$/

export function viewerFor(viewers: ViewerPlugin[] | undefined, name: string, mime?: string): ViewerPlugin | undefined {
  const ext = name.includes('.') ? name.slice(name.lastIndexOf('.')).toLowerCase() : ''
  const m = (mime ?? '').split(';')[0].trim().toLowerCase()
  return (viewers ?? []).find((v) =>
    v.extensions.some((e) => e.toLowerCase() === ext) ||
    v.mimes.some((p) => (p.endsWith('/*') ? m.startsWith(p.slice(0, -1)) : p === m)))
}

export function previewKind(viewers: ViewerPlugin[] | undefined, name: string, mime?: string): PreviewKind | null {
  if (viewerFor(viewers, name, mime)) return 'plugin'
  const m = (mime ?? '').toLowerCase()
  if (IMAGE.test(m)) return 'image'
  if (m.startsWith('video/')) return 'video'
  if (m.startsWith('audio/')) return 'audio'
  if (m === 'application/pdf' || m.startsWith('text/')) return 'frame'
  return null
}
