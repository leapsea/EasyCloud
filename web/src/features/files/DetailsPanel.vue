<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { LoaderCircle, X } from 'lucide-vue-next'
import { filesApi, type FileDetails } from '@/api/files'
import FileIcon from '@/components/FileIcon.vue'
import { formatSize, formatTime, parentPath } from '@/components/format'

// Side panel with everything known about one file or folder: timestamps
// (including creation time when the filesystem records it) and media
// properties read from the container headers.
const props = defineProps<{ path: string }>()
const emit = defineEmits<{ close: []; navigate: [dir: string] }>()
const { t, locale } = useI18n()

const d = ref<FileDetails | null>(null)
const usage = ref<{ bytes: number; files: number } | null>(null)
const loading = ref(false)
const failed = ref(false)
let seq = 0
watch(() => props.path, async (p) => {
  const my = ++seq
  loading.value = true; failed.value = false; usage.value = null
  try {
    const r = await filesApi.details(p)
    if (my !== seq) return
    d.value = r
    if (r.isDir) usage.value = await filesApi.usage(p).catch(() => null)
  } catch {
    if (my === seq) { failed.value = true; d.value = null }
  } finally {
    if (my === seq) loading.value = false
  }
}, { immediate: true })

const num = (n: number) => n.toLocaleString(locale.value)
function duration(s: number) {
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60)
  return h ? `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}` : `${m}:${String(sec).padStart(2, '0')}`
}
function bitrate(b?: number) {
  if (!b) return ''
  return b >= 1e6 ? `${(b / 1e6).toFixed(b >= 1e7 ? 0 : 1)} Mbps` : `${Math.round(b / 1e3)} kbps`
}
// Common names for a frame height, so "1920 × 1080" also reads as 1080p.
function resolutionName(w: number, h: number) {
  const short = Math.min(w, h)
  if (short >= 2100) return '8K'
  if (short >= 1500) return '4K'
  if (short >= 1350) return '1440p'
  if (short >= 1000) return '1080p'
  if (short >= 700) return '720p'
  if (short >= 470) return '480p'
  return ''
}
function channels(n?: number) {
  if (!n) return ''
  return n === 1 ? t('details.mono') : n === 2 ? t('details.stereo') : n === 6 ? '5.1' : n === 8 ? '7.1' : t('details.channelsN', { n })
}
const codecName = (c?: string, p?: string) => [c?.toUpperCase().replace('AVC1', 'H.264').replace('H264', 'H.264').replace('HEVC', 'H.265').replace('HVC1', 'H.265').replace('MP4A', 'AAC'), p].filter(Boolean).join(' · ')

type Row = [string, string]
const sections = computed(() => {
  const x = d.value
  if (!x) return []
  const out: { title: string; rows: Row[] }[] = []
  const basic: Row[] = [
    [t('details.type'), x.isDir ? t('details.folder') : x.mime || t('details.unknownType')],
    [t('details.size'), x.isDir ? (usage.value ? formatSize(usage.value.bytes) : '…') : t('details.withNote', { main: formatSize(x.size), note: t('details.bytes', { n: num(x.size) }) })],
  ]
  if (x.isDir && usage.value) basic.push([t('details.contains'), t('details.files', { n: num(usage.value.files) })])
  basic.push(
    [t('details.created'), x.createdAt ? formatTime(x.createdAt) : t('details.unavailable')],
    [t('details.modified'), formatTime(x.modTime)],
  )
  out.push({ title: t('details.basic'), rows: basic })
  if (x.image) {
    out.push({ title: t('details.imageSection'), rows: [
      [t('details.dimensions'), `${x.image.width} × ${x.image.height}`],
      ...(x.image.format ? [[t('details.format'), x.image.format.toUpperCase()] as Row] : []),
    ] })
  }
  const m = x.media
  if (m?.video) {
    const v = m.video
    const rows: Row[] = []
    if (m.duration) rows.push([t('details.duration'), duration(m.duration)])
    if (v.width && v.height) {
      const name = resolutionName(v.width, v.height)
      rows.push([t('details.resolution'), name ? t('details.withNote', { main: `${v.width} × ${v.height}`, note: name }) : `${v.width} × ${v.height}`])
    }
    if (v.fps) rows.push([t('details.fps'), `${+v.fps.toFixed(3)} fps`])
    if (v.codec) rows.push([t('details.codec'), codecName(v.codec, v.profile)])
    if (v.bitrate) rows.push([t('details.bitrate'), bitrate(v.bitrate)])
    if (v.pixFmt) rows.push([t('details.pixFmt'), v.pixFmt])
    out.push({ title: t('details.videoSection'), rows })
  }
  if (m?.audio) {
    const a = m.audio
    const rows: Row[] = []
    if (!m.video && m.duration) rows.push([t('details.duration'), duration(m.duration)])
    if (a.codec) rows.push([t('details.codec'), codecName(a.codec)])
    if (a.sampleRate) rows.push([t('details.sampleRate'), `${(a.sampleRate / 1000).toFixed(a.sampleRate % 1000 ? 1 : 0)} kHz`])
    if (a.channels) rows.push([t('details.channels'), channels(a.channels)])
    if (a.bitrate) rows.push([t('details.bitrate'), bitrate(a.bitrate)])
    out.push({ title: t('details.audioSection'), rows })
  }
  if (m && (m.container || m.bitrate)) {
    const rows: Row[] = []
    if (m.container) rows.push([t('details.container'), m.container.split(',')[0].toUpperCase()])
    if (m.bitrate) rows.push([t('details.totalBitrate'), bitrate(m.bitrate)])
    out.push({ title: t('details.fileSection'), rows })
  }
  return out
})
const thumb = computed(() => (d.value?.thumb ? `/api/files/thumb?size=512&path=${encodeURIComponent(d.value.path)}` : ''))
</script>

<template>
  <aside class="flex h-full w-full flex-col overflow-hidden bg-[var(--surface)]" :aria-label="$t('details.title')">
    <header class="flex h-12 shrink-0 items-center gap-2 border-b border-[var(--border)] px-4">
      <h2 class="text-sm font-semibold">{{ $t('details.title') }}</h2>
      <button class="ml-auto rounded-md p-1 text-[var(--text-muted)] hover:bg-[var(--surface-sunken)] hover:text-[var(--text)]" :aria-label="$t('common.close')" :title="$t('common.close')" @click="emit('close')">
        <X class="h-4 w-4" />
      </button>
    </header>
    <div class="min-h-0 flex-1 overflow-y-auto">
      <div v-if="loading && !d" class="flex justify-center p-10"><LoaderCircle class="h-6 w-6 animate-spin text-[var(--text-muted)]" /></div>
      <p v-else-if="failed" class="muted p-6 text-center text-sm">{{ $t('details.failed') }}</p>
      <template v-else-if="d">
        <div class="flex flex-col items-center gap-3 border-b border-[var(--border)] px-4 pb-4 pt-5">
          <div class="flex h-36 w-full items-center justify-center overflow-hidden rounded-lg bg-[var(--surface-muted)]">
            <img v-if="thumb" :src="thumb" alt="" class="h-full w-full object-contain" />
            <FileIcon v-else :entry="d" :size="56" />
          </div>
          <p class="w-full break-all text-center text-sm font-medium leading-5">{{ d.name || '/' }}</p>
          <button
            v-if="d.path !== '/'" class="max-w-full truncate rounded px-1.5 text-xs text-[var(--text-muted)] hover:bg-[var(--surface-sunken)] hover:text-[var(--text)]"
            :title="$t('details.openLocation')" @click="emit('navigate', parentPath(d.path))"
          >{{ parentPath(d.path) }}</button>
        </div>
        <section v-for="s in sections" :key="s.title" class="border-b border-[var(--border)] px-4 py-3 last:border-0">
          <h3 class="mb-2 text-xs font-medium text-[var(--text-muted)]">{{ s.title }}</h3>
          <dl class="grid grid-cols-[5.5rem_1fr] gap-x-3 gap-y-1.5 text-[13px]">
            <template v-for="[k, v] in s.rows" :key="k">
              <dt class="text-[var(--text-muted)]">{{ k }}</dt>
              <dd class="min-w-0 break-words tabular-nums">{{ v }}</dd>
            </template>
          </dl>
        </section>
        <p v-if="d.media?.source === 'builtin'" class="muted px-4 pb-4 text-[11px] leading-4">{{ $t('details.builtinNote') }}</p>
      </template>
    </div>
  </aside>
</template>
