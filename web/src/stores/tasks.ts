import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'
import { request } from '@/api/client'
import { realtime } from '@/api/realtime'
import { useAuth } from './auth'

export interface Task {
  id: string; kind: 'copy' | 'move' | 'extract'; status: 'queued' | 'running' | 'done' | 'failed' | 'cancelled'
  params: { sources: string[]; dest?: string }; doneBytes: number; totalBytes: number; message?: string; result?: string[]
  createdAt: string; updatedAt: string
}

// Background copy/move/extract tasks; progress arrives over the user's own WebSocket topic.
export const useTasks = defineStore('tasks', () => {
  const items = ref<Task[]>([])
  const listeners = new Set<(t: Task) => void>()
  const auth = useAuth()
  let unsub: (() => void) | undefined

  async function load() {
    try { items.value = (await request<{ items: Task[] }>('GET', '/api/tasks')).items } catch { /* signed out */ }
  }
  function upsert(t: Task) {
    const i = items.value.findIndex((x) => x.id === t.id)
    const prev = i >= 0 ? items.value[i] : undefined
    if (i >= 0) items.value[i] = t
    else items.value.unshift(t)
    if (prev?.status !== t.status && ['done', 'failed', 'cancelled'].includes(t.status)) listeners.forEach((f) => f(t))
  }
  watch(() => auth.user?.id, (id) => {
    unsub?.()
    items.value = []
    if (id) { unsub = realtime.subscribe(`tasks:${id}`, (d) => upsert(d as Task), load); load() }
  }, { immediate: true })

  async function create(kind: Task['kind'], sources: string[], dest?: string) {
    const t = await request<Task>('POST', '/api/tasks', { kind, sources, dest, onConflict: 'rename' })
    upsert(t)
    return t
  }
  const cancel = (id: string) => request<void>('POST', `/api/tasks/${id}/cancel`)
  async function clearFinished() { await request<void>('DELETE', '/api/tasks'); items.value = items.value.filter((t) => t.status === 'queued' || t.status === 'running') }
  const visible = computed(() => items.value.filter((t) => Date.now() - +new Date(t.updatedAt) < 3600_000 || t.status === 'running' || t.status === 'queued'))
  const onFinished = (fn: (t: Task) => void) => { listeners.add(fn); return () => listeners.delete(fn) }

  return { items, visible, create, cancel, clearFinished, onFinished, load }
})
