import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { FileEntry } from '@/api/files'

// The audio player lives at the app level (GlobalAudio in App.vue), so it
// keeps playing while minimized and while the user moves between pages.
export const useAudio = defineStore('audio', () => {
  const entry = ref<FileEntry | null>(null)
  const minimized = ref(false)
  function open(e: FileEntry) { entry.value = e; minimized.value = false }
  function close() { entry.value = null; minimized.value = false }
  return { entry, minimized, open, close }
})
