<script setup lang="ts">
import { computed, watch } from 'vue'
import { useAudio } from '@/stores/audio'
import Modal from '@/components/Modal.vue'
import VideoPlayer from './VideoPlayer.vue'
import { filesApi, type FileEntry } from '@/api/files'

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
  return 'frame' // pdf, text: rendered in a sandboxed frame (server sends CSP sandbox)
})
</script>

<template>
  <VideoPlayer :entry="kind === 'video' ? entry : null" @close="emit('close')" @download="(e) => emit('download', e)" />
  <Modal v-model:open="open" :title="entry?.name ?? ''" wide>
    <div class="flex max-h-[60vh] items-center justify-center overflow-hidden rounded-lg bg-black/5">
      <img v-if="kind === 'image'" :src="url" :alt="entry?.name" class="max-h-[60vh] object-contain" />
      <iframe v-else :src="url" sandbox="" class="h-[60vh] w-full bg-white" :title="entry?.name" />
    </div>
    <template #footer>
      <button class="btn" @click="entry && emit('download', entry)">{{ $t('common.download') }}</button>
      <button class="btn btn-primary" @click="emit('close')">{{ $t('common.close') }}</button>
    </template>
  </Modal>
</template>
