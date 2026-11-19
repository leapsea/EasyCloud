<script setup lang="ts">
import { ChevronLeft, ChevronRight, Download, X } from 'lucide-vue-next'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { filesApi, type FileEntry } from '@/api/files'
import { formatSize } from '@/components/format'

const props = defineProps<{ images: FileEntry[]; start: number | null }>()
const emit = defineEmits<{ close: []; download: [e: FileEntry] }>()
const idx = ref(0)
const original = ref(false)
const loaded = ref(false)
watch(() => props.start, (s) => { if (s !== null) { idx.value = s; original.value = false } }, { immediate: true })
const cur = computed(() => (props.start === null ? null : props.images[idx.value]))
const src = computed(() => {
  const e = cur.value
  if (!e) return ''
  return original.value || !e.thumb ? filesApi.downloadUrl(e.path, true) : `/api/files/thumb?size=1024&path=${encodeURIComponent(e.path)}`
})
watch(src, () => (loaded.value = false))

function go(d: number) {
  if (!props.images.length) return
  idx.value = (idx.value + d + props.images.length) % props.images.length
  original.value = false
}
function onKey(e: KeyboardEvent) {
  if (props.start === null) return
  if (e.key === 'ArrowRight') go(1)
  else if (e.key === 'ArrowLeft') go(-1)
  else if (e.key === 'Escape') emit('close')
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
// Preload neighbours for instant paging.
watch(cur, () => {
  for (const d of [1, -1]) {
    const n = props.images[(idx.value + d + props.images.length) % props.images.length]
    if (n?.thumb) new Image().src = `/api/files/thumb?size=1024&path=${encodeURIComponent(n.path)}`
  }
})
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="cur" class="fixed inset-0 z-50 flex flex-col bg-black/90 text-white" role="dialog" aria-modal="true" :aria-label="cur.name">
        <header class="flex items-center gap-3 px-4 py-3 text-sm">
          <span class="min-w-0 flex-1 truncate">{{ cur.name }}</span>
          <span class="text-white/60 tabular-nums">{{ idx + 1 }} / {{ images.length }}</span>
          <span v-if="cur.width" class="text-white/60">{{ cur.width }}×{{ cur.height }} · {{ formatSize(cur.size) }}</span>
          <button v-if="cur.thumb" class="rounded px-2 py-1 hover:bg-white/10" @click="original = !original">{{ original ? $t('lightbox.preview') : $t('lightbox.original') }}</button>
          <button class="inline-flex items-center gap-1 rounded px-2 py-1 hover:bg-white/10" @click="emit('download', cur)"><Download class="h-4 w-4" />{{ $t('common.download') }}</button>
          <button class="rounded px-2 py-1 hover:bg-white/10" :aria-label="$t('common.close')" @click="emit('close')"><X class="h-5 w-5" /></button>
        </header>
        <div class="relative flex min-h-0 flex-1 items-center justify-center" @click.self="emit('close')">
          <img :key="src" :src="src" :alt="cur.name" class="max-h-full max-w-full object-contain transition-opacity duration-200" :class="loaded ? 'opacity-100' : 'opacity-0'" @load="loaded = true" />
          <span v-if="!loaded" class="absolute text-white/50">{{ $t('common.loading') }}</span>
          <button v-if="images.length > 1" class="absolute left-2 top-1/2 -translate-y-1/2 rounded-full bg-white/10 p-2.5 backdrop-blur hover:bg-white/20" :aria-label="$t('lightbox.prev')" @click="go(-1)"><ChevronLeft class="h-6 w-6" /></button>
          <button v-if="images.length > 1" class="absolute right-2 top-1/2 -translate-y-1/2 rounded-full bg-white/10 p-2.5 backdrop-blur hover:bg-white/20" :aria-label="$t('lightbox.next')" @click="go(1)"><ChevronRight class="h-6 w-6" /></button>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>
