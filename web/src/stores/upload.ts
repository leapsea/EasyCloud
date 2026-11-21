import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import * as tus from 'tus-js-client'
import { ApiError } from '@/api/client'
import { settingsApi } from '@/api/files'

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

function toApiError(err: Error): ApiError | Error {
  const res = (err as tus.DetailedError).originalResponse
  if (!res) return err
  try {
    const b = JSON.parse(res.getBody())
    return new ApiError(res.getStatus(), b.code, b.detail, b.action)
  } catch {
    return new ApiError(res.getStatus(), res.getStatus() === 404 ? 'NOT_FOUND' : 'INTERNAL')
  }
}

export const useUploads = defineStore('uploads', () => {
  const items = ref<UploadItem[]>([])
  const uploads = new Map<number, tus.Upload>()
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
      const reactiveItem = items.value[items.value.length - 1]
      let lastT = performance.now(), lastB = 0
      const up = new tus.Upload(file, {
        endpoint: '/api/upload/',
        chunkSize,
        retryDelays: [0, 1000, 3000, 5000, 10000],
        removeFingerprintOnSuccess: true,
        // Resume only an upload of the same file into the same folder: the
        // default fingerprint ignores the folder, so the same file sent to
        // another folder would resume the old upload and land in the old one.
        fingerprint: async (f: File) => ['ec-tus', dir, f.name, f.type, f.size, f.lastModified].join('|'),
        metadata: { filename: file.name, dir, onConflict: 'rename' },
        onProgress(sent) {
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
          if (p) reactiveItem.finalPath = p
        },
        onSuccess() {
          reactiveItem.state = 'done'
          reactiveItem.sent = reactiveItem.size
          uploads.delete(reactiveItem.id)
          listeners.forEach((f) => f(dir))
          pump()
        },
        onError(err) {
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
      uploads.set(item.id, up)
    }
    pump()
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
      up.findPreviousUploads().then((prev) => {
        if (prev.length) up.resumeFromPreviousUpload(prev[0])
        up.start()
      })
    }
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
    items.value = items.value.filter((x) => x.id !== id)
    pump()
  }
  function clearFinished() { items.value = items.value.filter((i) => i.state !== 'done') }

  return { items, active, totalProgress, add, pause, resume, cancel, clearFinished, onFinished }
})
