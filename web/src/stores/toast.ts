import { t } from '@/i18n'
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ApiError, errorMessage, type ErrorAction } from '@/api/client'

export type ToastKind = 'success' | 'info' | 'warn' | 'error'

// A toast is a short title plus an optional one- or two-line detail; errors
// may carry a fix-it action and the request ID for support.
export interface Toast {
  id: number
  kind: ToastKind
  title: string
  text?: string
  action?: ErrorAction
  requestId?: string
}

const TTL: Record<ToastKind, number> = { success: 4000, info: 5000, warn: 6000, error: 9000 }
const MAX = 6 // older ones are dropped beyond this

export const useToast = defineStore('toast', () => {
  const items = ref<Toast[]>([])
  let seq = 0
  // Auto-dismiss timers; paused while the pointer is over the stack.
  const timers = new Map<number, { handle?: number; left: number; started: number }>()
  let paused = false

  function arm(id: number) {
    const tm = timers.get(id)
    if (!tm || paused) return
    tm.started = performance.now()
    tm.handle = window.setTimeout(() => dismiss(id), tm.left)
  }

  function push(x: Omit<Toast, 'id' | 'title'> & { title?: string }, ttl?: number) {
    const id = ++seq
    const title = x.title || t(`toast.title.${x.kind}`)
    items.value = [...items.value, { ...x, title, id }].slice(-MAX)
    timers.set(id, { left: ttl ?? TTL[x.kind], started: 0 })
    arm(id)
    return id
  }
  function dismiss(id: number) {
    const tm = timers.get(id)
    if (tm?.handle) clearTimeout(tm.handle)
    timers.delete(id)
    items.value = items.value.filter((x) => x.id !== id)
  }
  function pause() {
    if (paused) return
    paused = true
    const now = performance.now()
    for (const tm of timers.values()) {
      if (tm.handle) { clearTimeout(tm.handle); tm.handle = undefined; tm.left = Math.max(800, tm.left - (now - tm.started)) }
    }
  }
  function resume() {
    if (!paused) return
    paused = false
    for (const id of timers.keys()) arm(id)
  }

  const success = (title: string, text?: string) => push({ kind: 'success', title, text })
  const info = (title: string, text?: string) => push({ kind: 'info', title, text })
  const warn = (title: string, text?: string) => push({ kind: 'warn', title, text })
  // title: what failed ("保存失败"); the detail is the server's explanation.
  function error(e: unknown, isAdmin = true, title?: string) {
    const ae = e instanceof ApiError ? e : undefined
    let action = ae?.action
    let text = errorMessage(e)
    if (action?.role === 'admin' && !isAdmin) { action = undefined; text += t('common.contactAdmin') }
    push({ kind: 'error', title, text, action, requestId: ae?.requestId })
  }
  return { items, push, dismiss, pause, resume, success, info, warn, error }
})
