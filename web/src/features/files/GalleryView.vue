<script setup lang="ts">
import { galleryCols } from './galleryPref'
import FileIcon from '@/components/FileIcon.vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { FileEntry } from '@/api/files'
import { formatSize } from '@/components/format'

// Virtualized waterfall: columns are assigned greedily (shortest first) using
// the image dimensions from the index, so tiles never jump while loading.
const props = defineProps<{ entries: FileEntry[]; selected: Set<string> }>()
const emit = defineEmits<{ open: [e: FileEntry]; select: [e: FileEntry, ev: MouseEvent] }>()

const scroller = ref<HTMLElement>()
const width = ref(800)
const scrollTop = ref(0)
const viewport = ref(600)
const GAP = 8
const CAPTION = 26

// The chosen count wins, but tiles never shrink below ~80px on narrow screens.
const cols = computed(() => {
  const fit = Math.max(2, Math.floor((width.value + GAP) / (200 + GAP)))
  if (!galleryCols.value) return fit
  return Math.max(1, Math.min(galleryCols.value, Math.floor((width.value + GAP) / (80 + GAP))))
})
const colW = computed(() => (width.value - GAP * (cols.value - 1)) / cols.value)
const layout = computed(() => {
  const heights = new Array(cols.value).fill(0)
  return props.entries.map((e) => {
    let h: number
    if (e.isDir || !e.thumb) h = colW.value * 0.62
    else if (e.width && e.height) h = Math.min(colW.value * 2.2, colW.value * (e.height / e.width))
    else h = colW.value
    let c = 0
    for (let i = 1; i < heights.length; i++) if (heights[i] < heights[c]) c = i
    const top = heights[c]
    heights[c] += h + CAPTION + GAP
    return { e, top, left: c * (colW.value + GAP), h }
  })
})
const total = computed(() => Math.max(0, ...layout.value.map((l) => l.top + l.h + CAPTION)))
const visible = computed(() => {
  const lo = scrollTop.value - 600, hi = scrollTop.value + viewport.value + 600
  return layout.value.filter((l) => l.top + l.h + CAPTION >= lo && l.top <= hi)
})

let ro: ResizeObserver | undefined
onMounted(() => {
  ro = new ResizeObserver(() => {
    width.value = scroller.value!.clientWidth - 16
    viewport.value = scroller.value!.clientHeight
  })
  ro.observe(scroller.value!)
})
onBeforeUnmount(() => ro?.disconnect())
const thumbUrl = (e: FileEntry) => `/api/files/thumb?size=${colW.value > 256 ? 512 : 256}&path=${encodeURIComponent(e.path)}`
</script>

<template>
  <div ref="scroller" class="h-full overflow-auto p-2" @scroll="scrollTop = ($event.target as HTMLElement).scrollTop">
    <div class="relative" :style="{ height: total + 'px' }">
      <div
        v-for="l in visible" :key="l.e.path" :data-path="l.e.path"
        class="absolute cursor-pointer select-none overflow-hidden rounded-lg border transition-shadow hover:shadow-md"
        :class="selected.has(l.e.path) ? 'border-brand-500 ring-2 ring-brand-500/40' : 'border-[var(--border)]'"
        :style="{ top: l.top + 'px', left: l.left + 'px', width: colW + 'px' }"
        @click="emit('select', l.e, $event)" @dblclick="emit('open', l.e)"
      >
        <div class="flex items-center justify-center bg-[var(--surface-muted)]" :style="{ height: l.h + 'px' }">
          <img v-if="l.e.thumb" :src="thumbUrl(l.e)" :alt="l.e.name" loading="lazy" decoding="async" class="h-full w-full object-cover" @error="($event.target as HTMLImageElement).style.display = 'none'" />
          <FileIcon v-else :entry="l.e" :size="44" />
        </div>
        <div class="flex items-center gap-1 px-2 text-xs" :style="{ height: CAPTION + 'px' }">
          <span class="min-w-0 flex-1 truncate" :title="l.e.name">{{ l.e.name }}</span>
          <span v-if="!l.e.isDir" class="muted tabular-nums">{{ formatSize(l.e.size) }}</span>
        </div>
      </div>
    </div>
  </div>
</template>
