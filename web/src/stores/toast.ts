import { t } from '@/i18n'
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ApiError, errorMessage, type ErrorAction } from '@/api/client'

export interface Toast { id: number; kind: 'info' | 'error' | 'success'; text: string; action?: ErrorAction; requestId?: string }

export const useToast = defineStore('toast', () => {
  const items = ref<Toast[]>([])
  let seq = 0

  function push(t: Omit<Toast, 'id'>, ttl = 4500) {
    const id = ++seq
    items.value.push({ ...t, id })
    window.setTimeout(() => dismiss(id), t.kind === 'error' ? ttl * 2 : ttl)
  }
  function dismiss(id: number) { items.value = items.value.filter((t) => t.id !== id) }

  const success = (text: string) => push({ kind: 'success', text })
  function error(e: unknown, isAdmin = true) {
    const ae = e instanceof ApiError ? e : undefined
    let action = ae?.action
    let text = errorMessage(e)
    if (action?.role === 'admin' && !isAdmin) { action = undefined; text += t('common.contactAdmin') }
    push({ kind: 'error', text, action, requestId: ae?.requestId })
  }
  return { items, push, dismiss, success, error }
})
