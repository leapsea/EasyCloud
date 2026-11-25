<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { LoaderCircle } from 'lucide-vue-next'
import { formatSize } from './format'

// Plain-text preview that never loads a whole large file (a 1 GB log would
// freeze the tab). It reads the file in chunks with HTTP Range requests:
// the start first, "load more" appends the next chunk, and "show end" jumps
// to the last chunk (where new lines of a log are). Small files load at once.
const props = defineProps<{ url: string; size: number }>()

const CHUNK = 512 * 1024
const text = ref('')
const loaded = ref(0)         // bytes read from the start (head mode)
const tail = ref(false)       // showing the end of the file instead
const busy = ref(false)
const error = ref('')
let decoder = new TextDecoder('utf-8')
let seq = 0

async function fetchRange(start: number, end: number): Promise<Uint8Array> {
  const res = await fetch(props.url, { headers: { Range: `bytes=${start}-${end}` } })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const buf = new Uint8Array(await res.arrayBuffer())
  // A server ignoring Range answers 200 with everything: keep only the slice asked for.
  return res.status === 206 ? buf : buf.subarray(start, end + 1)
}

async function loadMore() {
  if (busy.value || loaded.value >= props.size) return
  const my = seq
  busy.value = true
  error.value = ''
  try {
    const end = Math.min(props.size, loaded.value + CHUNK) - 1
    const bytes = await fetchRange(loaded.value, end)
    if (my !== seq) return
    loaded.value = end + 1
    // stream: a multi-byte character split across chunks is completed by the next one
    text.value += decoder.decode(bytes, { stream: loaded.value < props.size })
  } catch (e) { if (my === seq) error.value = String(e) } finally { if (my === seq) busy.value = false }
}

async function showEnd() {
  const my = ++seq
  busy.value = true
  error.value = ''
  tail.value = true
  try {
    const start = Math.max(0, props.size - CHUNK)
    const bytes = await fetchRange(start, props.size - 1)
    if (my !== seq) return
    let s = new TextDecoder('utf-8').decode(bytes)
    if (start > 0) s = s.slice(s.indexOf('\n') + 1) // drop the partial first line
    text.value = s
  } catch (e) { if (my === seq) error.value = String(e) } finally { if (my === seq) busy.value = false }
}

function showStart() {
  seq++
  tail.value = false
  text.value = ''
  loaded.value = 0
  decoder = new TextDecoder('utf-8')
  busy.value = false
  loadMore()
}

watch(() => props.url, showStart, { immediate: true })

const partial = computed(() => props.size > CHUNK)
const status = computed(() => {
  if (!partial.value) return ''
  return tail.value ? 'tail' : loaded.value >= props.size ? 'all' : 'head'
})
</script>

<template>
  <div class="flex h-full min-h-0 flex-col">
    <div v-if="partial" class="flex flex-wrap items-center gap-2 border-b border-[var(--border)] bg-[var(--surface-muted)] px-3 py-1.5 text-xs">
      <span class="muted">
        <template v-if="status === 'tail'">{{ $t('textViewer.tail', { size: formatSize(size), shown: formatSize(Math.min(size, CHUNK)) }) }}</template>
        <template v-else-if="status === 'head'">{{ $t('textViewer.head', { size: formatSize(size), shown: formatSize(loaded) }) }}</template>
        <template v-else>{{ $t('textViewer.all', { size: formatSize(size) }) }}</template>
      </span>
      <span class="ml-auto flex gap-1.5">
        <button v-if="status === 'head'" class="btn btn-sm" :disabled="busy" @click="loadMore">{{ $t('textViewer.more', { size: formatSize(CHUNK) }) }}</button>
        <button v-if="status !== 'tail'" class="btn btn-sm" :disabled="busy" @click="showEnd">{{ $t('textViewer.end') }}</button>
        <button v-else class="btn btn-sm" :disabled="busy" @click="showStart">{{ $t('textViewer.start') }}</button>
      </span>
    </div>
    <p v-if="error" class="p-3 text-sm text-red-600">{{ error }}</p>
    <pre class="min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-words bg-[var(--surface)] p-3 font-mono text-xs leading-5 text-[var(--text)]">{{ text }}</pre>
    <div v-if="busy" class="muted flex items-center justify-center gap-2 py-2 text-xs"><LoaderCircle class="h-3.5 w-3.5 animate-spin" />{{ $t('common.loading') }}</div>
  </div>
</template>
