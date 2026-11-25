<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { onBeforeUnmount, ref, watch } from 'vue'
import Modal from '@/components/Modal.vue'
import { fileToken, type ViewerPlugin } from '@/api/plugins'
import type { FileEntry } from '@/api/files'
import { errorMessage } from '@/api/client'
import { useTheme } from '@/theme/theme'
const { t } = useI18n()

// Runs a viewer plugin in an opaque-origin sandbox: it gets a short-lived
// capability URL for this one file and nothing else (no cookies, no app access).
// resolveUrl: where the capability URL comes from (default: the signed-in
// token endpoint; the public share page passes its share-scoped one).
const props = defineProps<{ entry: FileEntry | null; plugin: ViewerPlugin | null; resolveUrl?: (path: string) => Promise<{ url: string }> }>()
const emit = defineEmits<{ close: []; download: [e: FileEntry] }>()
const open = ref(false)
const frame = ref<HTMLIFrameElement>()
const err = ref('')
const loading = ref(true)
watch(() => props.entry, (e) => { open.value = !!e; err.value = ''; loading.value = true })
watch(open, (o) => { if (!o) emit('close') })

async function onMessage(ev: MessageEvent) {
  if (!frame.value || ev.source !== frame.value.contentWindow) return
  const m = ev.data as { source?: string; type?: string; message?: string }
  if (m?.source !== 'easycloud-viewer' || !props.entry || !props.plugin) return
  if (m.type === 'loaded') {
    try {
      const { url } = await (props.resolveUrl ?? fileToken)(props.entry.path)
      const dark = useTheme().dark.value
      frame.value.contentWindow!.postMessage({
        type: 'init', entry: props.plugin.entry, theme: dark ? 'dark' : 'light',
        file: { name: props.entry.name, mime: props.entry.mime ?? '', size: props.entry.size, path: props.entry.path, url: new URL(url, location.href).href },
      }, '*')
    } catch (e) { err.value = errorMessage(e); loading.value = false }
  } else if (m.type === 'ready') loading.value = false
  else if (m.type === 'error') { err.value = m.message ?? t('viewer.failed'); loading.value = false }
  else if (m.type === 'close') open.value = false
  else if (m.type === 'download') emit('download', props.entry)
}
window.addEventListener('message', onMessage)
onBeforeUnmount(() => window.removeEventListener('message', onMessage))
</script>

<template>
  <Modal v-model:open="open" :title="entry?.name ?? ''" :description="plugin ? $t('viewer.by', { name: plugin.label }) : ''" fill>
    <div class="relative min-h-0 flex-1 overflow-hidden rounded-lg border border-[var(--border)]">
      <iframe v-if="entry && plugin" ref="frame" src="/plugins/_host.html" sandbox="allow-scripts" class="h-full w-full" :title="entry.name" referrerpolicy="no-referrer" />
      <div v-if="loading && !err" class="muted absolute inset-0 flex items-center justify-center text-sm">{{ $t('common.loading') }}</div>
      <p v-if="err" class="absolute inset-x-0 top-4 text-center text-sm text-red-600">{{ err }}</p>
    </div>
    <template #footer>
      <button class="btn" @click="entry && emit('download', entry)">{{ $t('common.download') }}</button>
      <button class="btn btn-primary" @click="open = false">{{ $t('common.close') }}</button>
    </template>
  </Modal>
</template>
