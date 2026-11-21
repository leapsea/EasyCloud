import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import * as tus from 'tus-js-client'
import { ApiError } from '@/api/client'
import { settingsApi } from '@/api/files'
import { useToast } from '@/stores/toast'
import { formatSize } from '@/components/format'
import { t } from '@/i18n'

export type UploadState = 'queued' | 'uploading' | 'paused' | 'done' | 'error'

export interface UploadItem {
  id: number
  name: string
  dir: string
  size: number
  sent: number
  state: UploadState
  error?: ApiError | Error
  finalPath?: string
  speed: number // bytes/s, smoothed
}

const MAX_PARALLEL = 3
// A reverse proxy in front of EasyCloud may cap the request body (nginx
// defaults to 1 MB, the default chunk is 16 MB) and answer 413 itself. The
// chunk size is then halved down to this floor and the upload resumed.
const MIN_CHUNK = 512 * 1024
// Watchdog: a chunk whose bytes stop moving for this long while it is still
// being sent is abandoned and the upload resumed from the server's offset —
// what pausing and resuming by hand does. Some proxy setups leave a follow-up
// PATCH hanging before it reaches the server. Waiting for the response after
// the body was fully sent is not watched: the server may be moving a big file.
const STALL_MS = 20_000
// The body was sent but no answer came back: a proxy that swallowed the
// request. Long, because the server may be copying a big file into place.
const AWAIT_MS = 5 * 60_000
const MAX_STALL_RESTARTS = 5

const statusOf = (err: Error) => (err as tus.DetailedError).originalResponse?.getStatus() ?? 0

function toApiError(err: Error): ApiError | Error {
  const res = (err as tus.DetailedError).originalResponse
  if (!res) return err
  try {
    const b = JSON.parse(res.getBody())
    if (b && typeof b.code === 'string') return new ApiError(res.getStatus(), b.code, b.detail, b.action)
  } catch { /* not ours: e.g. an HTML page from a reverse proxy */ }
  const s = res.getStatus()
  if (s === 404) return new ApiError(s, 'NOT_FOUND')
  // Not an EasyCloud answer: something in between (reverse proxy, firewall) refused it.
  if (s === 413) return new ApiError(s, 'PROXY_BODY_TOO_LARGE', undefined, { key: 'uploadChunk', label: 'Upload chunk size', route: '/admin/settings', focus: 'upload.chunk_size', role: 'admin' })
  return new ApiError(s, 'PROXY_REJECTED', { status: s })
}

export const useUploads = defineStore('uploads', () => {
  const items = ref<UploadItem[]>([])
  const uploads = new Map<number, tus.Upload>()
  const fileOf = new Map<number, File>()
  const chunks = new Map<number, number>() // per item, shrinks on 413
  let proxyHinted = false
  // Per item: last time bytes moved, bytes the server has confirmed, restarts.
  const watch = new Map<number, { at: number; accepted: number; restarts: number }>()
  let watchTimer: number | undefined
  const listeners = new Set<(dir: string) => void>()
  let seq = 0
  let chunkSize = 16 * 1024 * 1024
  let configLoaded = false

  const active = computed(() => items.value.filter((i) => i.state === 'uploading' || i.state === 'queued'))
  const totalProgress = computed(() => {
    const live = items.value.filter((i) => i.state !== 'error')
    const size = live.reduce((a, i) => a + i.size, 0)
    return size ? live.reduce((a, i) => a + i.sent, 0) / size : 0
  })

  async function loadConfig() {
    if (configLoaded) return
    try { chunkSize = (await settingsApi.clientConfig()).uploadChunkSize; configLoaded = true } catch { /* keep default */ }
  }

  function onFinished(fn: (dir: string) => void) { listeners.add(fn); return () => listeners.delete(fn) }

  async function add(files: File[], dir: string) {
    await loadConfig()
    for (const file of files) {
      const item: UploadItem = { id: ++seq, name: file.name, dir, size: file.size, sent: 0, state: 'queued', speed: 0 }
      items.value.push(item)
      fileOf.set(item.id, file)
      chunks.set(item.id, chunkSize)
      uploads.set(item.id, makeUpload(items.value[items.value.length - 1], file, dir))
    }
    pump()
  }

  function makeUpload(reactiveItem: UploadItem, file: File, dir: string): tus.Upload {
      let lastT = performance.now(), lastB = 0
      return new tus.Upload(file, {
        endpoint: '/api/upload/',
        chunkSize: chunks.get(reactiveItem.id) ?? chunkSize,
        retryDelays: [0, 1000, 3000, 5000, 10000],
        removeFingerprintOnSuccess: true,
        // Resume only an upload of the same file into the same folder: the
        // default fingerprint ignores the folder, so the same file sent to
        // another folder would resume the old upload and land in the old one.
        fingerprint: async (f: File) => ['ec-tus', dir, f.name, f.type, f.size, f.lastModified].join('|'),
        metadata: { filename: file.name, dir, onConflict: 'rename' },
        onChunkComplete(_len, accepted) {
          const w = watch.get(reactiveItem.id)
          if (w) { w.at = performance.now(); w.accepted = accepted; w.restarts = 0 }
        },
        onProgress(sent) {
          const w = watch.get(reactiveItem.id)
          if (w) w.at = performance.now()
          const now = performance.now()
          if (now - lastT > 500) {
            const inst = ((sent - lastB) * 1000) / (now - lastT)
            reactiveItem.speed = reactiveItem.speed ? reactiveItem.speed * 0.7 + inst * 0.3 : inst
            lastT = now; lastB = sent
          }
          reactiveItem.sent = sent
        },
        onAfterResponse(_req, res) {
          const p = res.getHeader('X-Easycloud-Path')
          if (p) { try { reactiveItem.finalPath = decodeURIComponent(p) } catch { reactiveItem.finalPath = p } }
        },
        onSuccess() {
          fileOf.delete(reactiveItem.id)
          reactiveItem.state = 'done'
          reactiveItem.sent = reactiveItem.size
          uploads.delete(reactiveItem.id)
          listeners.forEach((f) => f(dir))
          pump()
        },
        onError(err) {
          if (statusOf(err) === 413 && shrinkAndRetry(reactiveItem)) return
          reactiveItem.state = 'error'
          reactiveItem.error = toApiError(err)
          pump()
        },
        // Do not retry client errors such as 413/404/409; they will not fix themselves.
        onShouldRetry(err) {
          const s = (err as tus.DetailedError).originalResponse?.getStatus() ?? 0
          return s === 0 || s >= 500 || s === 423
        },
      })
  }

  // 413 from a proxy: halve the chunk (also for later uploads) and resume the
  // same upload (same fingerprint, so tus finds it) with the smaller chunks.
  function shrinkAndRetry(item: UploadItem): boolean {
    // Halve what was actually sent (a small file sends less than a chunk).
    const sent = Math.min(chunks.get(item.id) ?? chunkSize, Math.max(item.size - item.sent, 1))
    if (sent <= MIN_CHUNK) return false
    const next = Math.max(MIN_CHUNK, Math.floor(sent / 2))
    chunks.set(item.id, next)
    chunkSize = Math.min(chunkSize, next)
    if (!proxyHinted) {
      proxyHinted = true
      useToast().push({ kind: 'info', text: t('upload.proxyShrink', { size: formatSize(next) }) }, 9000)
    }
    const file = fileOf.get(item.id)
    if (!file) return false
    uploads.set(item.id, makeUpload(item, file, item.dir))
    item.state = 'queued'
    item.error = undefined
    queueMicrotask(pump)
    return true
  }

  function pump() {
    let running = items.value.filter((i) => i.state === 'uploading').length
    for (const i of items.value) {
      if (running >= MAX_PARALLEL) break
      if (i.state !== 'queued') continue
      const up = uploads.get(i.id)
      if (!up) continue
      i.state = 'uploading'
      running++
      const prev = watch.get(i.id)
      watch.set(i.id, { at: performance.now(), accepted: i.sent, restarts: prev?.restarts ?? 0 })
      startWatchdog()
      up.findPreviousUploads().then((prev) => {
        if (prev.length) up.resumeFromPreviousUpload(prev[0])
        up.start()
      })
    }
  }

  function startWatchdog() {
    if (watchTimer) return
    watchTimer = window.setInterval(checkStalls, 5000)
  }
  function checkStalls() {
    const now = performance.now()
    let any = false
    for (const i of items.value) {
      if (i.state !== 'uploading') continue
      any = true
      const w = watch.get(i.id)
      const up = uploads.get(i.id)
      if (!w || !up || now - w.at < STALL_MS) continue
      const chunk = chunks.get(i.id) ?? chunkSize
      const bodySent = i.sent >= Math.min(w.accepted + chunk, i.size)
      if (bodySent && now - w.at < AWAIT_MS) continue // waiting for the server's answer
      if (w.restarts >= MAX_STALL_RESTARTS) {
        up.abort()
        i.state = 'error'
        i.error = new ApiError(0, 'UPLOAD_STALLED')
        pump()
        continue
      }
      w.restarts++
      w.at = now
      i.sent = w.accepted // the unconfirmed part of the chunk is sent again
      // abort() keeps the upload URL, so start() resumes with a HEAD.
      up.abort().then(() => { if (i.state === 'uploading') up.start() })
    }
    if (!any) { clearInterval(watchTimer); watchTimer = undefined }
  }

  function pause(id: number) {
    const i = items.value.find((x) => x.id === id)
    if (!i || i.state !== 'uploading') return
    uploads.get(id)?.abort()
    i.state = 'paused'
    pump()
  }
  function resume(id: number) {
    const i = items.value.find((x) => x.id === id)
    if (!i || (i.state !== 'paused' && i.state !== 'error')) return
    i.state = 'queued'
    i.error = undefined
    pump()
  }
  function cancel(id: number) {
    uploads.get(id)?.abort(true)
    uploads.delete(id)
    fileOf.delete(id)
    chunks.delete(id)
    watch.delete(id)
    items.value = items.value.filter((x) => x.id !== id)
    pump()
  }
  function clearFinished() { items.value = items.value.filter((i) => i.state !== 'done') }

  return { items, active, totalProgress, add, pause, resume, cancel, clearFinished, onFinished }
})
