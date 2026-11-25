<script setup lang="ts">
import { onBeforeUnmount, onMounted } from 'vue'
import { Download, X } from 'lucide-vue-next'
import { filesApi } from '@/api/files'
import OfficeEditor from './OfficeEditor.vue'

// Opens a document over the current page instead of navigating to it, so
// closing it returns to exactly where the user was (a folder, or a filtered
// category list).
const props = defineProps<{ path: string | null }>()
const emit = defineEmits<{ close: [] }>()

function onKey(e: KeyboardEvent) { if (e.key === 'Escape' && props.path) emit('close') }
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
function download() {
  if (!props.path) return
  const a = document.createElement('a')
  a.href = filesApi.downloadUrl(props.path)
  a.download = props.path.split('/').pop() ?? ''
  a.click()
}
</script>

<template>
  <Teleport to="body">
    <div v-if="path" class="fixed inset-0 z-50 flex flex-col bg-[var(--surface)]" role="dialog" aria-modal="true" :aria-label="path.split('/').pop()">
      <header class="flex h-11 shrink-0 items-center gap-2 border-b border-[var(--border)] px-3 text-sm">
        <span class="min-w-0 flex-1 truncate font-medium" :title="path">{{ path.split('/').pop() }}</span>
        <button class="btn btn-sm" @click="download"><Download class="h-3.5 w-3.5" />{{ $t('common.download') }}</button>
        <button class="btn btn-sm" :aria-label="$t('common.close')" @click="emit('close')"><X class="h-3.5 w-3.5" />{{ $t('common.close') }}</button>
      </header>
      <OfficeEditor :key="path" :path="path" class="min-h-0 flex-1" />
    </div>
  </Teleport>
</template>
