<script setup lang="ts">
import { computed, watch } from 'vue'
import { useAudio } from '@/stores/audio'
import Modal from '@/components/Modal.vue'
import VideoPlayer from './VideoPlayer.vue'
import { filesApi, type FileEntry } from '@/api/files'
import TextViewer from '@/components/TextViewer.vue'
import { isPdf, isTextFile } from '@/components/fileKinds'

// V1 built-in viewers; V3 replaces this with the MIME-registered plugin loader.
const props = defineProps<{ entry: FileEntry | null }>()
const emit = defineEmits<{ close: []; download: [e: FileEntry] }>()

// Video has its own full player; audio goes to the app-wide player (it keeps
// playing when minimized or on other pages); the rest uses the generic dialog.
const open = computed({ get: () => !!props.entry && kind.value !== 'video' && kind.value !== 'audio', set: (v) => { if (!v) emit('close') } })
const audio = useAudio()
watch(() => props.entry, (e) => { if (e && kind.value === 'audio') { audio.open(e); emit('close') } })
const url = computed(() => (props.entry ? filesApi.downloadUrl(props.entry.path, true) : ''))
const kind = computed(() => {
  const m = props.entry?.mime ?? ''
  if (m.startsWith('image/')) return 'image'
  if (m.startsWith('video/')) return 'video'
  if (m.startsWith('audio/')) return 'audio'
  const n = props.entry?.name ?? ''
  if (isPdf(n, m)) return 'pdf'
  if (isTextFile(n, m)) return 'text' // read in chunks: large logs must not load whole
  return 'frame'
})
</script>

<template>
  <VideoPlayer :entry="kind === 'video' ? entry : null" @close="emit('close')" @download="(e) => emit('download', e)" />
  <Modal v-model:open="open" :title="entry?.name ?? ''" :wide="kind === 'image'" :fill="kind !== 'image'">
    <div class="flex items-center justify-center overflow-hidden rounded-lg bg-black/5" :class="kind === 'image' ? 'max-h-[60vh]' : 'min-h-0 flex-1'">
      <img v-if="kind === 'image'" :src="url" :alt="entry?.name" class="max-h-[60vh] object-contain" />
      <TextViewer v-else-if="kind === 'text' && entry" :url="url" :size="entry.size" class="h-full w-full rounded-lg border border-[var(--border)]" />
      <!-- PDFs need the browser's viewer, which a sandboxed frame does not allow. -->
      <iframe v-else-if="kind === 'pdf'" :src="url" class="h-full w-full bg-white" :title="entry?.name" />
      <iframe v-else :src="url" sandbox="" class="h-full w-full bg-white" :title="entry?.name" />
    </div>
    <template #footer>
      <button class="btn" @click="entry && emit('download', entry)">{{ $t('common.download') }}</button>
      <button class="btn btn-primary" @click="emit('close')">{{ $t('common.close') }}</button>
    </template>
  </Modal>
</template>
