<script setup lang="ts">
import AudioPlayer from '@/features/files/AudioPlayer.vue'
import type { PreviewKind } from './preview'
import TextViewer from '@/components/TextViewer.vue'
import { isPdf } from '@/components/fileKinds'

// The built-in online preview of one shared file (plugins open in PluginViewer).
defineProps<{ kind: Exclude<PreviewKind, 'plugin'>; url: string; name: string; size?: number; mime?: string }>()

// Videos start playing; where the browser refuses playback with sound, they
// start muted and the native controls offer unmuting.
function autoplay(e: Event) {
  const v = e.target as HTMLVideoElement
  v.play().catch(() => { v.muted = true; v.play().catch(() => {}) })
}
</script>

<template>
  <img v-if="kind === 'image'" :src="url" :alt="name" class="mx-auto max-h-[70vh] max-w-full rounded-lg object-contain" />
  <video
    v-else-if="kind === 'video'" :src="url" controls playsinline preload="metadata"
    class="mx-auto max-h-[70vh] w-full rounded-lg bg-black" @loadedmetadata="autoplay"
  />
  <AudioPlayer v-else-if="kind === 'audio'" :src="url" :name="name" />
  <!-- PDFs need the browser's viewer (not allowed in a sandboxed frame); text is read in chunks. -->
  <iframe v-else-if="isPdf(name, mime)" :src="url" :title="name" class="h-[70vh] w-full rounded-lg border border-[var(--border)] bg-white" />
  <TextViewer v-else :url="url" :size="size ?? 0" class="h-[70vh] w-full rounded-lg border border-[var(--border)]" />
</template>
