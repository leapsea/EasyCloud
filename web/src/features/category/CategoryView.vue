<script setup lang="ts">
import OfficeOverlay from '@/features/office/OfficeOverlay.vue'
import { isPdf, isTextFile } from '@/components/fileKinds'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useMediaQuery } from '@vueuse/core'
import { ChevronRight, Download, ExternalLink, FolderOpen, Info, LayoutGrid, List, LoaderCircle, Puzzle } from 'lucide-vue-next'
import { useAuth } from '@/stores/auth'
import { ContextMenuContent, ContextMenuItem, ContextMenuPortal, ContextMenuRoot, ContextMenuSeparator, ContextMenuTrigger } from 'reka-ui'
import PageHeader from '@/components/PageHeader.vue'
import FileIcon from '@/components/FileIcon.vue'
import { filesApi, type FileEntry } from '@/api/files'
import type { ViewerPlugin } from '@/api/plugins'
import { formatSize, formatTime, parentPath } from '@/components/format'
import { usePlugins, isOffice } from '@/stores/plugins'
import { useToast } from '@/stores/toast'
import GalleryView from '@/features/files/GalleryView.vue'
import PreviewModal from '@/features/files/PreviewModal.vue'
import ImageLightbox from '@/features/files/ImageLightbox.vue'
import DetailsPanel from '@/features/files/DetailsPanel.vue'
import PluginViewer from '@/plugins/PluginViewer.vue'

// Files of one kind (images, videos, audio, documents) from every folder the
// user can read, served from the file index, newest first by default and
// loaded page by page while scrolling.
const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const toast = useToast()
const plugins = usePlugins()
const auth = useAuth()

// Videos without a same-name cover picture need the FFmpeg plugin for a
// thumbnail; the server says whether one can render frames.
const videoFrames = ref(true)
function installCoverPlugin() {
  router.push({ path: '/admin/plugins', query: { highlight: 'ffmpeg-thumbnail' } })
}

type Kind = 'image' | 'video' | 'audio' | 'doc' | 'other'
const kind = computed(() => route.params.kind as Kind)

const SORTS = [
  { key: 'mtime-desc', sort: 'mtime', order: 'desc', label: 'category.sort.newest' },
  { key: 'mtime-asc', sort: 'mtime', order: 'asc', label: 'category.sort.oldest' },
  { key: 'name-asc', sort: 'name', order: 'asc', label: 'category.sort.nameAsc' },
  { key: 'name-desc', sort: 'name', order: 'desc', label: 'category.sort.nameDesc' },
  { key: 'size-desc', sort: 'size', order: 'desc', label: 'category.sort.largest' },
  { key: 'size-asc', sort: 'size', order: 'asc', label: 'category.sort.smallest' },
] as const
const sortKey = ref<(typeof SORTS)[number]['key']>(kind.value === 'other' ? 'size-desc' : 'mtime-desc')

// Type filter tags per kind (keys match the server's Category.Types; ""
// is all, "other" is everything outside the listed types).
const TYPES: Record<Kind, { key: string; label: string }[]> = {
  image: [{ key: 'jpg', label: 'JPG' }, { key: 'png', label: 'PNG' }, { key: 'gif', label: 'GIF' }, { key: 'webp', label: 'WebP' }, { key: 'heic', label: 'HEIC' }, { key: 'raw', label: 'RAW' }],
  video: [{ key: 'mp4', label: 'MP4' }, { key: 'mkv', label: 'MKV' }, { key: 'mov', label: 'MOV' }, { key: 'avi', label: 'AVI' }, { key: 'ts', label: 'TS' }],
  audio: [{ key: 'mp3', label: 'MP3' }, { key: 'flac', label: 'FLAC' }, { key: 'wav', label: 'WAV' }, { key: 'aac', label: 'AAC' }, { key: 'ape', label: 'APE' }],
  other: [{ key: 'image', label: 'category.type.diskImage' }, { key: 'archive', label: 'category.type.archive' }, { key: 'installer', label: 'category.type.installer' }],
  doc: [{ key: 'pdf', label: 'PDF' }, { key: 'txt', label: 'TXT' }, { key: 'word', label: 'Word' }, { key: 'excel', label: 'Excel' }, { key: 'ppt', label: 'PPT' }],
}
const typeKey = ref('')
const typeTags = computed(() => [
  { key: '', label: t('category.type.all') },
  ...(TYPES[kind.value] ?? []).map((x) => ({ key: x.key, label: x.label.startsWith('category.') ? t(x.label) : x.label })),
  { key: 'other', label: t('category.type.other') },
])
// "Other" (disk images, archives, ...) is about big files: largest first.
watch(kind, (k) => { typeKey.value = ''; sortKey.value = k === 'other' ? 'size-desc' : 'mtime-desc' })

// View per kind: pictures and videos default to the gallery, the rest to a list.
const VIEW_KEY = 'ec.category.view'
function readViews(): Record<string, 'grid' | 'list'> {
  try { return JSON.parse(localStorage.getItem(VIEW_KEY) ?? '{}') } catch { return {} }
}
const views = ref(readViews())
const view = computed({
  get: () => views.value[kind.value] ?? (kind.value === 'image' || kind.value === 'video' ? 'grid' : 'list'),
  set: (v) => {
    views.value = { ...views.value, [kind.value]: v }
    try { localStorage.setItem(VIEW_KEY, JSON.stringify(views.value)) } catch { /* private mode */ }
  },
})

// Selection (single, for highlighting and the details panel).
const selected = ref(new Set<string>())
function select(e: FileEntry) { selected.value = new Set([e.path]) }

const PAGE = 100
const items = ref<FileEntry[]>([])
const total = ref(0)
const loading = ref(false)
const done = ref(false)
let seq = 0
async function load(reset = false) {
  if (reset) { seq++; items.value = []; total.value = 0; done.value = false; selected.value = new Set() }
  if (loading.value && !reset) return
  if (done.value) return
  const my = seq
  loading.value = true
  const s = SORTS.find((x) => x.key === sortKey.value)!
  try {
    const r = await filesApi.category(kind.value, { type: typeKey.value, sort: s.sort, order: s.order, offset: items.value.length, limit: PAGE })
    if (my !== seq) return
    items.value = [...items.value, ...r.items]
    total.value = r.total
    if (r.videoFrames !== undefined) videoFrames.value = r.videoFrames
    done.value = items.value.length >= r.total || r.items.length < PAGE
  } catch (e) {
    if (my === seq) { toast.error(e); done.value = true }
  } finally {
    if (my === seq) loading.value = false
  }
}
watch([kind, typeKey, sortKey], () => load(true), { immediate: true })

// List view: load the next page when the end of the list scrolls into view.
const sentinel = ref<HTMLElement>()
let io: IntersectionObserver | undefined
watch(sentinel, (el) => {
  io?.disconnect()
  if (!el) return
  io = new IntersectionObserver((es) => { if (es.some((x) => x.isIntersecting)) load() }, { rootMargin: '400px' })
  io.observe(el)
})
onBeforeUnmount(() => io?.disconnect())


// Opening follows the file manager: plugin viewers, Office, the lightbox, the
// built-in preview (video, audio, PDF, text), otherwise a download.
const preview = ref<FileEntry | null>(null)
const officePath = ref<string | null>(null) // document open in the office editor overlay
const pluginView = ref<{ e: FileEntry; p: ViewerPlugin } | null>(null)
const lightbox = ref<number | null>(null)
const images = computed(() => items.value.filter((x) => (x.mime ?? '').startsWith('image/') && (x.thumb || /^image\/(jpeg|png|gif|webp|svg)/.test(x.mime ?? ''))))
async function open(e: FileEntry) {
  await plugins.load()
  if (plugins.office && isOffice(e.name)) { officePath.value = e.path; return } // over the page: closing it keeps the list as it was
  const p = plugins.viewerFor(e.name, e.mime)
  if (p) { pluginView.value = { e, p }; return }
  const i = images.value.findIndex((x) => x.path === e.path)
  if (i >= 0) { lightbox.value = i; return }
  if (/^(video|audio)\//.test(e.mime ?? '') || isPdf(e.name, e.mime) || isTextFile(e.name, e.mime)) { preview.value = e; return }
  download(e)
}
function download(e: FileEntry) {
  const a = document.createElement('a')
  a.href = filesApi.downloadUrl(e.path)
  a.download = e.name
  a.click()
}
function openLocation(e: FileEntry) { router.push({ path: '/files', query: { path: parentPath(e.path) } }) }

// Context menu target (rows and tiles carry data-path).
const ctxEntry = ref<FileEntry | null>(null)
function onContextMenu(ev: MouseEvent) {
  const el = (ev.target as HTMLElement).closest<HTMLElement>('[data-path]')
  ctxEntry.value = el ? items.value.find((x) => x.path === el.dataset.path) ?? null : null
  if (ctxEntry.value) select(ctxEntry.value)
}
const menuItem = 'flex cursor-default items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] outline-none data-[disabled]:opacity-40 data-[highlighted]:bg-[var(--surface-muted)]'

// Details panel, docked on wide screens like in the file manager.
const detailsPath = ref<string | null>(null)
const dockDetails = useMediaQuery('(min-width: 1024px)')
watch(selected, (s) => { if (detailsPath.value && s.size === 1) detailsPath.value = [...s][0] })
watch(kind, () => { detailsPath.value = null })

const empty = computed(() => !loading.value && !items.value.length)
const thumbUrl = (e: FileEntry) => `/api/files/thumb?size=128&path=${encodeURIComponent(e.path)}`
</script>

<template>
  <section class="flex h-full flex-col">
    <PageHeader :title="$t(`nav.cat.${kind}`)" :subtitle="total ? $t('category.count', { n: total.toLocaleString() }) : ''">
      <template #actions>
        <div class="hidden h-8 shrink-0 items-center rounded-lg border border-[var(--border)] bg-[var(--surface)] p-0.5 md:flex" role="radiogroup" :aria-label="$t('category.type.label')">
          <button
            v-for="tg in typeTags" :key="tg.key" role="radio" :aria-checked="typeKey === tg.key"
            class="flex h-full items-center whitespace-nowrap rounded-md px-2.5 text-[13px]"
            :class="typeKey === tg.key ? 'bg-[var(--surface-sunken)] font-medium text-brand-600' : 'text-[var(--text-muted)] hover:text-[var(--text)]'"
            @click="typeKey = tg.key"
          >{{ tg.label }}</button>
        </div>
        <select v-model="sortKey" class="input input-sm !w-auto" :aria-label="$t('category.sortLabel')">
          <option v-for="s in SORTS" :key="s.key" :value="s.key">{{ $t(s.label) }}</option>
        </select>
        <div class="flex h-8 shrink-0 items-center rounded-lg border border-[var(--border)] bg-[var(--surface)] p-0.5" role="group" :aria-label="$t('files.view')">
          <button class="flex h-full items-center rounded-md px-2" :class="view === 'list' ? 'bg-[var(--surface-sunken)] text-brand-600' : 'text-[var(--text-muted)] hover:text-[var(--text)]'" :title="$t('files.listView')" :aria-label="$t('files.listView')" @click="view = 'list'"><List class="h-4 w-4" /></button>
          <button class="flex h-full items-center rounded-md px-2" :class="view === 'grid' ? 'bg-[var(--surface-sunken)] text-brand-600' : 'text-[var(--text-muted)] hover:text-[var(--text)]'" :title="$t('files.gridView')" :aria-label="$t('files.gridView')" @click="view = 'grid'"><LayoutGrid class="h-4 w-4" /></button>
        </div>
      </template>
    </PageHeader>

    <component
      :is="auth.user?.isAdmin ? 'button' : 'div'" v-if="kind === 'video' && !videoFrames"
      class="mb-2 flex w-full items-center gap-2 rounded-lg border border-amber-300/60 bg-amber-50 px-3 py-2 text-left text-[13px] text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-200"
      :class="auth.user?.isAdmin ? 'hover:bg-amber-100 dark:hover:bg-amber-500/15' : ''"
      @click="auth.user?.isAdmin && installCoverPlugin()"
    >
      <Puzzle class="h-4 w-4 shrink-0" />
      <span class="min-w-0 flex-1">{{ $t('category.coverPlugin') }} {{ auth.user?.isAdmin ? $t('category.coverPluginAdmin') : $t('category.coverPluginUser') }}</span>
      <ChevronRight v-if="auth.user?.isAdmin" class="h-4 w-4 shrink-0" />
    </component>

    <!-- Narrow screens: the tags get their own scrollable row. -->
    <div class="-mt-1 mb-2 flex gap-1.5 overflow-x-auto md:hidden" role="radiogroup" :aria-label="$t('category.type.label')">
      <button
        v-for="tg in typeTags" :key="tg.key" role="radio" :aria-checked="typeKey === tg.key"
        class="h-7 shrink-0 whitespace-nowrap rounded-full border px-3 text-xs"
        :class="typeKey === tg.key ? 'border-brand-500 bg-brand-50 font-medium text-brand-600' : 'border-[var(--border)] bg-[var(--surface)] text-[var(--text-muted)]'"
        @click="typeKey = tg.key"
      >{{ tg.label }}</button>
    </div>

    <div class="flex min-h-0 flex-1 gap-3">
      <div class="card relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        <ContextMenuRoot>
          <ContextMenuTrigger as-child>
            <div class="flex min-h-0 flex-1 flex-col" @contextmenu="onContextMenu">
              <div v-if="empty" class="muted flex h-full flex-col items-center justify-center gap-2 p-10 text-center text-sm">
                <FolderOpen class="h-10 w-10 opacity-40" :stroke-width="1.25" />
                <p>{{ typeKey ? $t('category.empty.filtered') : $t(`category.empty.${kind}`) }}</p>
                <p class="text-xs">{{ $t('category.indexHint') }}</p>
              </div>
              <GalleryView
                v-else-if="view === 'grid'" :entries="items" :selected="selected" class="min-h-0 flex-1"
                @open="open" @select="(e) => select(e)" @more="load()"
              />
              <div v-else class="min-h-0 flex-1 overflow-auto">
                <div
                  v-for="e in items" :key="e.path" :data-path="e.path"
                  class="flex cursor-default select-none items-center gap-3 border-b border-[var(--border)] px-4 py-2 text-sm last:border-0"
                  :class="selected.has(e.path) ? 'bg-brand-50' : 'hover:bg-[var(--surface-muted)]'"
                  @click="select(e)" @dblclick="open(e)"
                >
                  <img v-if="e.thumb" :src="thumbUrl(e)" loading="lazy" decoding="async" alt="" class="h-8 w-8 shrink-0 rounded object-cover" />
                  <FileIcon v-else :entry="e" :size="22" />
                  <div class="min-w-0 flex-1">
                    <p class="truncate" :title="e.name">{{ e.name }}</p>
                    <button class="muted block max-w-full truncate text-left text-xs hover:text-[var(--text)] hover:underline" :title="$t('details.openLocation')" @click.stop="openLocation(e)">{{ parentPath(e.path) }}</button>
                  </div>
                  <span class="muted hidden w-20 shrink-0 text-right text-xs tabular-nums sm:block">{{ formatSize(e.size) }}</span>
                  <span class="muted hidden w-36 shrink-0 text-right text-xs tabular-nums md:block">{{ formatTime(e.modTime) }}</span>
                </div>
                <div v-if="!done" ref="sentinel" class="h-px" />
              </div>
              <div v-if="loading" class="pointer-events-none absolute inset-x-0 bottom-3 flex justify-center">
                <span class="flex items-center gap-2 rounded-full bg-[var(--surface)] px-3 py-1 text-xs shadow"><LoaderCircle class="h-3.5 w-3.5 animate-spin" />{{ $t('common.loading') }}</span>
              </div>
            </div>
          </ContextMenuTrigger>
          <ContextMenuPortal>
            <ContextMenuContent v-if="ctxEntry" class="z-50 min-w-44 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-1 shadow-[var(--shadow-pop)]">
              <ContextMenuItem :class="menuItem" @select="open(ctxEntry)"><ExternalLink class="h-3.5 w-3.5" />{{ $t('files.ctx.open') }}</ContextMenuItem>
              <ContextMenuItem :class="menuItem" @select="download(ctxEntry)"><Download class="h-3.5 w-3.5" />{{ $t('common.download') }}</ContextMenuItem>
              <ContextMenuItem :class="menuItem" @select="openLocation(ctxEntry)"><FolderOpen class="h-3.5 w-3.5" />{{ $t('details.openLocation') }}</ContextMenuItem>
              <ContextMenuSeparator class="my-1 h-px bg-[var(--border)]" />
              <ContextMenuItem :class="menuItem" @select="detailsPath = ctxEntry.path"><Info class="h-3.5 w-3.5" />{{ $t('files.ctx.details') }}</ContextMenuItem>
            </ContextMenuContent>
          </ContextMenuPortal>
        </ContextMenuRoot>
      </div>
      <div v-if="detailsPath && dockDetails" class="card w-80 shrink-0 overflow-hidden">
        <DetailsPanel :path="detailsPath" @close="detailsPath = null" @navigate="(d) => router.push({ path: '/files', query: { path: d } })" />
      </div>
    </div>
    <Teleport to="body">
      <div v-if="detailsPath && !dockDetails" class="fixed inset-0 z-40 bg-slate-900/30" @click="detailsPath = null" />
      <div v-if="detailsPath && !dockDetails" class="fixed inset-y-0 right-0 z-40 w-[min(22rem,100vw)] border-l border-[var(--border)] shadow-[var(--shadow-pop)]">
        <DetailsPanel :path="detailsPath" @close="detailsPath = null" @navigate="(d) => router.push({ path: '/files', query: { path: d } })" />
      </div>
    </Teleport>

    <OfficeOverlay :path="officePath" @close="officePath = null" />
    <PreviewModal :entry="preview" @close="preview = null" @download="download" />
    <PluginViewer :entry="pluginView?.e ?? null" :plugin="pluginView?.p ?? null" @close="pluginView = null" @download="download" />
    <ImageLightbox :images="images" :start="lightbox" @close="lightbox = null" @download="download" />
  </section>
</template>
