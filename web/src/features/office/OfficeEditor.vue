<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { TriangleAlert } from 'lucide-vue-next'
import { actionLabel, request, errorMessage, ApiError } from '@/api/client'
import { useAuth } from '@/stores/auth'

// The OnlyOffice-compatible editor for one file, filling its container.
// Used by the full-screen overlay (files stay where they are) and the
// standalone /office page.
const props = defineProps<{ path: string }>()
const { t } = useI18n()
const auth = useAuth()

const err = ref('')
const action = ref<ApiError['action']>()
// Why the Document Server may fail to fetch the file, shown above the editor.
const hint = ref<{ base: string } | null>(null)
const elId = `office-editor-${Math.random().toString(36).slice(2)}`
let editor: { destroyEditor?: () => void } | null = null

function loadScript(src: string) {
  return new Promise<void>((resolve, reject) => {
    if ((window as any).DocsAPI) return resolve()
    const s = document.createElement('script')
    s.src = src
    s.onload = () => resolve()
    s.onerror = () => reject(new Error(t('office.scriptFailed', { src })))
    document.head.appendChild(s)
  })
}

onMounted(async () => {
  try {
    const r = await request<{ server: string; config: Record<string, any>; base: string; hint?: string }>('POST', '/api/office/session', { path: props.path })
    if (r.hint === 'OFFICE_LOOPBACK_URL') hint.value = { base: r.base }
    await loadScript(`${r.server}/web-apps/apps/api/documents/api.js`)
    r.config.width = '100%'
    r.config.height = '100%'
    r.config.events = {
      // The Document Server reports problems here, e.g. that it could not
      // download the file (-4): usually the address EasyCloud gave it.
      onError: (e: { data?: { errorCode?: number; errorDescription?: string } }) => {
        const code = e?.data?.errorCode
        err.value = code === -4 ? t('office.downloadFailed', { base: r.base }) : t('office.editorError', { code: code ?? '?', detail: e?.data?.errorDescription ?? '' })
      },
    }
    editor = new (window as any).DocsAPI.DocEditor(elId, r.config)
  } catch (e) {
    err.value = errorMessage(e)
    if (e instanceof ApiError) action.value = e.action
  }
})
onBeforeUnmount(() => editor?.destroyEditor?.())
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <p v-if="hint" class="flex items-start gap-2 border-b border-amber-300/60 bg-amber-50 px-3 py-2 text-[13px] text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200">
      <TriangleAlert class="mt-0.5 h-4 w-4 shrink-0" />
      <span class="min-w-0 flex-1">{{ $t('office.loopbackHint', { base: hint.base }) }}
        <RouterLink v-if="auth.user?.isAdmin" class="ml-1 font-medium underline" to="/admin/plugins?highlight=onlyoffice">{{ $t('office.openPluginConfig') }}</RouterLink>
      </span>
    </p>
    <div v-if="err" class="m-auto max-w-lg p-6 text-center">
      <p class="whitespace-pre-line text-sm text-red-600">{{ err }}</p>
      <RouterLink v-if="action" class="btn btn-primary mt-3" :to="action.route">{{ actionLabel(action) }}</RouterLink>
    </div>
    <div v-show="!err" class="min-h-0 flex-1"><div :id="elId" /></div>
  </div>
</template>
