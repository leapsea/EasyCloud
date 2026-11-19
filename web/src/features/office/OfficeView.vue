<script setup lang="ts">
import { actionLabel } from '@/api/client'
import { useI18n } from 'vue-i18n'
import { ArrowLeft } from 'lucide-vue-next'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { request, errorMessage, ApiError } from '@/api/client'
import { parentPath } from '@/components/format'
const { t } = useI18n()

// Full-page editor backed by the OnlyOffice-compatible Document Server
// configured in the connector plugin.
const route = useRoute()
const router = useRouter()
const path = String(route.query.path ?? '')
const err = ref('')
const action = ref<ApiError['action']>()
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
    const { server, config } = await request<{ server: string; config: Record<string, any> }>('POST', '/api/office/session', { path })
    await loadScript(`${server}/web-apps/apps/api/documents/api.js`)
    config.width = '100%'
    config.height = '100%'
    editor = new (window as any).DocsAPI.DocEditor('office-editor', config)
  } catch (e) {
    err.value = errorMessage(e)
    if (e instanceof ApiError) action.value = e.action
  }
})
onBeforeUnmount(() => editor?.destroyEditor?.())
</script>

<template>
  <div class="flex h-screen flex-col">
    <header class="flex items-center gap-2 border-b border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm">
      <button class="btn btn-sm" @click="router.push({ path: '/files', query: { path: parentPath(path) } })"><ArrowLeft class="h-3.5 w-3.5" />{{ $t('common.back') }}</button>
      <span class="truncate font-medium">{{ path.split('/').pop() }}</span>
    </header>
    <div v-if="err" class="m-auto max-w-md p-6 text-center">
      <p class="text-red-600">{{ err }}</p>
      <RouterLink v-if="action" class="btn btn-primary mt-3" :to="action.route">{{ actionLabel(action) }}</RouterLink>
    </div>
    <div v-else class="min-h-0 flex-1"><div id="office-editor" /></div>
  </div>
</template>
