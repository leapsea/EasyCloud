<script setup lang="ts">
import AudioPlayer from '@/features/files/AudioPlayer.vue'
import type { PreviewKind } from './preview'

// The built-in online preview of one shared file (plugins open in PluginViewer).
defineProps<{ kind: Exclude<PreviewKind, 'plugin'>; url: string; name: string }>()

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
  <iframe v-else :src="url" sandbox="" :title="name" class="h-[70vh] w-full rounded-lg border border-[var(--border)] bg-white" />
</template>
