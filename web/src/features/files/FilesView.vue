<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { t as tr } from '@/i18n'
import { ArrowDown, ArrowUp, ChevronRight, Copy, Download, Ellipsis, FolderInput, FolderOpen, FolderPlus, LayoutGrid, List, PackageOpen, PencilLine, RotateCw, Search, Share2, Trash2, Upload, X } from 'lucide-vue-next'
import { DropdownMenuContent, DropdownMenuItem, DropdownMenuPortal, DropdownMenuRoot, DropdownMenuTrigger } from 'reka-ui'
import FileIcon from '@/components/FileIcon.vue'
import { computed, h, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { useMediaQuery } from '@vueuse/core'
import { filesApi, type FileEntry, type Listing } from '@/api/files'
import { realtime } from '@/api/realtime'
import { useToast } from '@/stores/toast'
import { useUploads } from '@/stores/upload'
import { useAuth } from '@/stores/auth'
import Modal from '@/components/Modal.vue'
import FolderPicker from './FolderPicker.vue'
import PreviewModal from './PreviewModal.vue'
import GalleryView from './GalleryView.vue'
import ImageLightbox from './ImageLightbox.vue'
import PluginViewer from '@/plugins/PluginViewer.vue'
import ShareDialog from '@/features/share/ShareDialog.vue'
import { useTasks } from '@/stores/tasks'
import { usePlugins, isOffice } from '@/stores/plugins'
import type { ViewerPlugin } from '@/api/plugins'
import { formatSize, formatTime, joinPath, parentPath } from '@/components/format'

const route = useRoute()
const router = useRouter()
const toast = useToast()
const uploads = useUploads()
const auth = useAuth()
const plugins = usePlugins()
plugins.load()

type ViewMode = 'list' | 'grid'
const readMode = (): ViewMode => { try { return localStorage.getItem('ec.view') === 'grid' ? 'grid' : 'list' } catch { return 'list' } }
const viewMode = ref<ViewMode>(readMode())

// Resizable columns (name takes the rest). Widths are remembered per browser.
type ResizableCol = 'size' | 'modified'
const COL_DEFAULT: Record<ResizableCol, number> = { size: 104, modified: 168 }
const COL_MIN = 64, COL_MAX = 420
const readCols = (): Record<ResizableCol, number> => {
  try { return { ...COL_DEFAULT, ...JSON.parse(localStorage.getItem('ec.cols') ?? '{}') } } catch { return { ...COL_DEFAULT } }
}
const colW = ref(readCols())
watch(colW, (w) => { try { localStorage.setItem('ec.cols', JSON.stringify(w)) } catch { /* private mode */ } }, { deep: true })
const wide = useMediaQuery('(min-width: 640px)')
const gridCols = computed(() => wide.value
  ? `2rem minmax(8rem,1fr) ${colW.value.size}px ${colW.value.modified}px`
  : `2rem minmax(0,1fr) ${colW.value.size}px`)
// The handle sits on a column's left edge: dragging left widens that column.
function startResize(ev: PointerEvent, col: ResizableCol) {
  ev.preventDefault()
  const startX = ev.clientX, startW = colW.value[col]
  const el = ev.target as HTMLElement
  el.setPointerCapture(ev.pointerId)
  document.body.classList.add('col-resizing')
  const move = (e: PointerEvent) => { colW.value[col] = Math.round(Math.min(COL_MAX, Math.max(COL_MIN, startW - (e.clientX - startX)))) }
  const up = () => {
    el.removeEventListener('pointermove', move)
    el.removeEventListener('pointerup', up)
    el.removeEventListener('pointercancel', up)
    document.body.classList.remove('col-resizing')
  }
  el.addEventListener('pointermove', move)
  el.addEventListener('pointerup', up)
  el.addEventListener('pointercancel', up)
}
const resetCol = (col: ResizableCol) => { colW.value[col] = COL_DEFAULT[col] }

// Sort indicator with a reserved slot, so clicking a header never changes its width.
const SortIcon = (props: { col: SortKey }) =>
  h('span', { class: 'inline-flex h-3 w-3 shrink-0 items-center', 'aria-hidden': 'true' },
    sort.value.key === props.col ? [h(sort.value.asc ? ArrowUp : ArrowDown, { class: 'h-3 w-3' })] : [])
watch(viewMode, (m) => { try { localStorage.setItem('ec.view', m) } catch { /* private mode */ } })

const dir = computed(() => (typeof route.query.path === 'string' && route.query.path) || (auth.user?.isAdmin ? '/' : auth.user?.home ?? '/'))
const access = computed(() => listing.value?.access ?? 'r')
const canWrite = computed(() => access.value === 'rw')

// Search mode: results replace the listing while a query is active.
const searchQ = ref('')
const searching = ref(false)
const searchHits = ref<FileEntry[] | null>(null)
let searchTimer: number | undefined
watch(searchQ, (q) => {
  clearTimeout(searchTimer)
  if (!q.trim()) { searchHits.value = null; return }
  searchTimer = window.setTimeout(async () => {
    searching.value = true
    try { searchHits.value = (await filesApi.search(q.trim())).items } catch (e) { toast.error(e, auth.user?.isAdmin) } finally { searching.value = false }
  }, 250)
})
function openHit(h: FileEntry) {
  query.value = ''
  if (h.isDir) navigate(h.path)
  else { navigate(parentPath(h.path)); open(h) }
}
const listing = ref<Listing | null>(null)
const loading = ref(false)
const selected = ref(new Set<string>())
const filter = ref('')
// One search box with a scope: "dir" filters the listing, "all" searches the index.
const query = ref('')
const scope = ref<'dir' | 'all'>('dir')
watch([query, scope], ([q, sc]) => {
  filter.value = sc === 'dir' ? q : ''
  searchQ.value = sc === 'all' ? q : ''
})
type SortKey = 'name' | 'size' | 'modTime'
const sort = ref<{ key: SortKey; asc: boolean }>({ key: 'name', asc: true })

const entries = computed(() => {
  const q = filter.value.trim().toLowerCase()
  const list = (listing.value?.entries ?? []).filter((e) => !q || e.name.toLowerCase().includes(q))
  const { key, asc } = sort.value
  return [...list].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    let c = 0
    if (key === 'name') c = a.name.localeCompare(b.name, 'zh-CN', { numeric: true })
    else if (key === 'size') c = a.size - b.size
    else c = a.modTime.localeCompare(b.modTime)
    return asc ? c : -c
  })
})
const selectedEntries = computed(() => entries.value.filter((e) => selected.value.has(e.path)))
// System directories (/user, personal spaces) cannot be deleted, moved or renamed; the server enforces this too.
const hasProtected = computed(() => selectedEntries.value.some((e) => e.protected))
const protectedTip = computed(() => tr('files.protectedTip'))
const crumbs = computed(() => {
  const segs = dir.value.split('/').filter(Boolean)
  return [{ name: tr('files.root'), path: '/' }, ...segs.map((s, i) => ({ name: s, path: '/' + segs.slice(0, i + 1).join('/') }))]
})

async function load(keepSelection = false) {
  loading.value = true
  try {
    listing.value = await filesApi.list(dir.value)
    if (!keepSelection) selected.value = new Set()
    else selected.value = new Set([...selected.value].filter((p) => listing.value!.entries.some((e) => e.path === p)))
  } catch (e) {
    toast.error(e, auth.user?.isAdmin)
    listing.value = { path: dir.value, access: 'nav', entries: [] }
  } finally {
    loading.value = false
  }
}

// Realtime: refresh when this directory changes (other tabs, other users, uploads).
let unsub: (() => void) | undefined
let refreshTimer: number | undefined
function scheduleRefresh() { clearTimeout(refreshTimer); refreshTimer = window.setTimeout(() => load(true), 150) }
watch(dir, (d) => {
  unsub?.()
  unsub = realtime.subscribe(`dir:${d}`, scheduleRefresh, scheduleRefresh)
  query.value = ''
  load()
}, { immediate: true })
const offUpload = uploads.onFinished((d) => { if (d === dir.value) scheduleRefresh() })
onBeforeUnmount(() => { unsub?.(); offUpload(); clearTimeout(refreshTimer) })

function navigate(path: string) { router.push({ path: '/files', query: { path } }) }

const preview = ref<FileEntry | null>(null)
const pluginView = ref<{ e: FileEntry; p: ViewerPlugin } | null>(null)
const shareTarget = ref<FileEntry | null>(null)
const tasks = useTasks()
const offTask = tasks.onFinished((t) => {
  if (t.status === 'done') toast.success(tr(`tasks.done.${t.kind}`))
  else if (t.status === 'failed') toast.push({ kind: 'error', text: tr('tasks.failed', { message: t.message ?? '' }) })
  scheduleRefresh()
})
onBeforeUnmount(offTask)
const isArchive = (e: FileEntry) => !e.isDir && /\.(zip|tar|tgz|tar\.gz)$/i.test(e.name)
async function extract(e: FileEntry) {
  try { await tasks.create('extract', [e.path]); toast.push({ kind: 'info', text: tr('files.extractStarted') }) } catch (err) { toast.error(err, auth.user?.isAdmin) }
}
function downloadZip() {
  const q = new URLSearchParams()
  for (const e of selectedEntries.value) q.append('path', e.path)
  const a = document.createElement('a')
  a.href = `/api/files/zip?${q}`
  a.click()
}
const lightbox = ref<number | null>(null)
const images = computed(() => entries.value.filter((x) => !x.isDir && (x.mime ?? '').startsWith('image/') && (x.thumb || /^image\/(jpeg|png|gif|webp|svg)/.test(x.mime ?? ''))))
async function open(e: FileEntry) {
  if (e.isDir) { navigate(e.path); return }
  await plugins.load()
  if (plugins.office && isOffice(e.name)) { router.push({ path: '/office', query: { path: e.path } }); return }
  const p = plugins.viewerFor(e.name, e.mime)
  if (p) { pluginView.value = { e, p }; return }
  const i = images.value.findIndex((x) => x.path === e.path)
  if (i >= 0) { lightbox.value = i; return }
  if (/^(video|audio)\//.test(e.mime ?? '') || e.mime?.includes('pdf') || e.mime?.startsWith('text/')) preview.value = e
  else download(e)
}
function download(e: FileEntry) {
  const a = document.createElement('a')
  a.href = filesApi.downloadUrl(e.path)
  a.download = e.name
  a.click()
}

// Selection
let anchor: string | null = null
function clickRow(e: FileEntry, ev: MouseEvent) {
  const s = new Set(ev.metaKey || ev.ctrlKey ? selected.value : [])
  if (ev.shiftKey && anchor) {
    const ids = entries.value.map((x) => x.path)
    const [a, b] = [ids.indexOf(anchor), ids.indexOf(e.path)].sort((x, y) => x - y)
    ids.slice(a, b + 1).forEach((p) => s.add(p))
  } else if (s.has(e.path) && (ev.metaKey || ev.ctrlKey)) {
    s.delete(e.path)
  } else {
    s.add(e.path)
    anchor = e.path
  }
  selected.value = s
}
function toggle(e: FileEntry) {
  const s = new Set(selected.value)
  s.has(e.path) ? s.delete(e.path) : s.add(e.path)
  selected.value = s
  anchor = e.path
}
const allSelected = computed(() => entries.value.length > 0 && selectedEntries.value.length === entries.value.length)
function toggleAll() { selected.value = allSelected.value ? new Set() : new Set(entries.value.map((e) => e.path)) }
function setSort(key: SortKey) { sort.value = { key, asc: sort.value.key === key ? !sort.value.asc : true } }

// Dialogs
const mkdirOpen = ref(false), mkdirName = ref('')
const renameOpen = ref(false), renameName = ref(''), renameTarget = ref<FileEntry | null>(null)
const deleteOpen = ref(false)
const picker = ref<{ open: boolean; mode: 'move' | 'copy' }>({ open: false, mode: 'move' })
const busy = ref(false)

async function run(fn: () => Promise<unknown>, ok?: string) {
  busy.value = true
  try { await fn(); if (ok) toast.success(ok); await load(true) } catch (e) { toast.error(e, auth.user?.isAdmin) } finally { busy.value = false }
}

const invalidName = (n: string) => !n.trim() || n.includes('/') || n === '.' || n === '..'
async function doMkdir() {
  if (invalidName(mkdirName.value)) return
  await run(() => filesApi.mkdir(joinPath(dir.value, mkdirName.value.trim())), tr('files.created'))
  mkdirOpen.value = false
}
function startRename() {
  const e = selectedEntries.value[0]
  if (!e) return
  renameTarget.value = e
  renameName.value = e.name
  renameOpen.value = true
  nextTick(() => {
    const el = document.getElementById('rename-input') as HTMLInputElement | null
    const dot = e.isDir ? -1 : e.name.lastIndexOf('.')
    el?.setSelectionRange(0, dot > 0 ? dot : e.name.length)
  })
}
async function doRename() {
  const e = renameTarget.value
  if (!e || invalidName(renameName.value) || renameName.value === e.name) { renameOpen.value = false; return }
  await run(() => filesApi.rename(e.path, renameName.value.trim()), tr('files.renamed'))
  renameOpen.value = false
}
async function doDelete() {
  const paths = selectedEntries.value.map((e) => e.path)
  await run(() => filesApi.trash(paths), tr('files.trashed', { n: paths.length }))
  deleteOpen.value = false
}
async function doTransfer(dest: string) {
  const paths = selectedEntries.value.map((e) => e.path)
  const mode = picker.value.mode
  // Folders and large selections run as background tasks with progress.
  if (selectedEntries.value.some((e) => e.isDir) || selectedEntries.value.reduce((a, e) => a + e.size, 0) > 256 << 20) {
    try { await tasks.create(mode, paths, dest); selected.value = new Set() } catch (e) { toast.error(e, auth.user?.isAdmin) }
    return
  }
  await run(() => (mode === 'move' ? filesApi.move(paths, dest, 'rename') : filesApi.copy(paths, dest, 'rename')),
    mode === 'move' ? tr('tasks.done.move') : tr('tasks.done.copy'))
}

// Upload: file input + drag and drop
const fileInput = ref<HTMLInputElement>()
function pickFiles() { fileInput.value?.click() }
function onFiles(ev: Event) {
  const input = ev.target as HTMLInputElement
  if (input.files?.length) uploads.add([...input.files], dir.value)
  input.value = ''
}
const dragging = ref(false)
let dragDepth = 0
function onDragEnter(ev: DragEvent) { if (ev.dataTransfer?.types.includes('Files')) { dragDepth++; dragging.value = true } }
function onDragLeave() { if (--dragDepth <= 0) { dragDepth = 0; dragging.value = false } }
function onDrop(ev: DragEvent) {
  dragDepth = 0
  dragging.value = false
  if (!canWrite.value) { toast.push({ kind: 'error', text: tr('files.noWrite') }); return }
  const files = [...(ev.dataTransfer?.files ?? [])]
  if (files.length) uploads.add(files, dir.value)
}

// Keyboard shortcuts
function onKey(ev: KeyboardEvent) {
  const t = ev.target as HTMLElement
  if (t.closest('input, textarea, [role="dialog"]')) return
  if ((ev.key === 'Delete' || ev.key === 'Backspace') && selected.value.size && canWrite.value && !hasProtected.value) { ev.preventDefault(); deleteOpen.value = true }
  else if (ev.key === 'F2' && selected.value.size === 1 && canWrite.value && !hasProtected.value) { ev.preventDefault(); startRename() }
  else if ((ev.metaKey || ev.ctrlKey) && ev.key === 'a') { ev.preventDefault(); selected.value = new Set(entries.value.map((e) => e.path)) }
  else if (ev.key === 'Escape') selected.value = new Set()
  else if (ev.key === 'Enter' && selectedEntries.value.length === 1) { ev.preventDefault(); open(selectedEntries.value[0]) }
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

// Virtualised rows: directories with tens of thousands of entries stay smooth.
const scroller = ref<HTMLElement>()
const virtualizer = useVirtualizer(computed(() => ({
  count: entries.value.length,
  getScrollElement: () => scroller.value ?? null,
  estimateSize: () => 44,
  overscan: 12,
})))
const rows = computed(() => virtualizer.value.getVirtualItems())
</script>

<template>
  <section
    class="flex h-full flex-col" @dragenter.prevent="onDragEnter" @dragover.prevent
    @dragleave.prevent="onDragLeave" @drop.prevent="onDrop"
  >
    <!-- Page header: where you are and what is selected. -->
    <PageHeader>
      <template #title>
      <nav class="no-scrollbar flex min-w-0 items-center gap-0.5 overflow-x-auto whitespace-nowrap text-[15px]" :aria-label="$t('files.path')">
        <template v-for="(c, i) in crumbs" :key="c.path">
          <ChevronRight v-if="i" class="h-3.5 w-3.5 shrink-0 text-[var(--text-muted)]" />
          <button
            class="shrink-0 rounded-md px-1.5 py-0.5 hover:bg-[var(--surface-sunken)]"
            :class="i === crumbs.length - 1 ? 'font-semibold' : 'text-[var(--text-muted)] hover:text-[var(--text)]'"
            @click="navigate(c.path)"
          >{{ c.name }}</button>
        </template>
      </nav>
      </template>
      <span v-if="selected.size" class="flex shrink-0 items-center gap-1 rounded-full bg-brand-50 py-0.5 pl-2.5 pr-1 text-xs text-brand-700">
        {{ $t('files.selected', { n: selected.size }) }}
        <button class="rounded-full p-0.5 hover:bg-brand-100" :title="$t('files.clearSelection')" :aria-label="$t('files.clearSelection')" @click="selected = new Set()"><X class="h-3 w-3" /></button>
      </span>
    </PageHeader>

    <div class="card relative flex min-h-0 flex-1 flex-col overflow-hidden">
      <!-- Card toolbar: actions left, search and view right. Fixed height and never wraps,
           so selecting rows cannot move the list between the two clicks of a double-click. -->
      <div class="flex h-12 shrink-0 items-center gap-2 border-b border-[var(--border)] px-3">
        <div class="no-scrollbar flex min-w-0 flex-1 items-center gap-2 overflow-x-auto whitespace-nowrap">
          <template v-if="!selected.size">
            <template v-if="canWrite">
              <button class="btn btn-primary" @click="pickFiles"><Upload class="h-4 w-4" />{{ $t('files.upload') }}</button>
              <button class="btn" @click="mkdirName = ''; mkdirOpen = true"><FolderPlus class="h-4 w-4" />{{ $t('files.newFolder') }}</button>
            </template>
            <span v-else-if="access === 'r'" class="badge badge-warning">{{ $t('files.readOnly') }}</span>
            <span v-else-if="access === 'nav'" class="muted text-xs">{{ $t('files.navOnly') }}</span>
            <button class="btn !px-2" :disabled="loading" :title="$t('files.refresh')" :aria-label="$t('files.refresh')" @click="load(true)"><RotateCw class="h-4 w-4" :class="{ 'animate-spin': loading }" /></button>
          </template>
          <template v-else>
            <!-- Frequent actions stay in the bar; the rest live under "More". -->
            <button v-if="canWrite" class="btn" :disabled="selected.size !== 1 || hasProtected" :title="hasProtected ? protectedTip : ''" @click="startRename"><PencilLine class="h-4 w-4" />{{ $t('files.rename') }}</button>
            <button v-if="canWrite" class="btn" :disabled="hasProtected" :title="hasProtected ? protectedTip : ''" @click="picker = { open: true, mode: 'move' }"><FolderInput class="h-4 w-4" />{{ $t('files.moveTo') }}</button>
            <button v-if="access !== 'nav'" class="btn" @click="picker = { open: true, mode: 'copy' }"><Copy class="h-4 w-4" />{{ $t('files.copyTo') }}</button>
            <DropdownMenuRoot>
              <DropdownMenuTrigger as-child>
                <button class="btn"><Ellipsis class="h-4 w-4" />{{ $t('files.more') }}</button>
              </DropdownMenuTrigger>
              <DropdownMenuPortal>
                <DropdownMenuContent align="start" :side-offset="6" class="z-50 min-w-40 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-1 shadow-[var(--shadow-pop)]">
                  <DropdownMenuItem v-if="selectedEntries.length === 1 && !selectedEntries[0].isDir" class="flex cursor-default items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] outline-none data-[disabled]:opacity-40 data-[highlighted]:bg-[var(--surface-muted)]" @select="download(selectedEntries[0])"><Download class="h-3.5 w-3.5" />{{ $t('common.download') }}</DropdownMenuItem>
                  <DropdownMenuItem v-else class="flex cursor-default items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] outline-none data-[disabled]:opacity-40 data-[highlighted]:bg-[var(--surface-muted)]" @select="downloadZip"><Download class="h-3.5 w-3.5" />{{ $t('files.downloadZip') }}</DropdownMenuItem>
                  <DropdownMenuItem v-if="access !== 'nav'" class="flex cursor-default items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] outline-none data-[disabled]:opacity-40 data-[highlighted]:bg-[var(--surface-muted)]" :disabled="selected.size !== 1" @select="shareTarget = selectedEntries[0]"><Share2 class="h-3.5 w-3.5" />{{ $t('files.share') }}</DropdownMenuItem>
                  <DropdownMenuItem v-if="canWrite && selectedEntries.length === 1 && isArchive(selectedEntries[0])" class="flex cursor-default items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] outline-none data-[disabled]:opacity-40 data-[highlighted]:bg-[var(--surface-muted)]" @select="extract(selectedEntries[0])"><PackageOpen class="h-3.5 w-3.5" />{{ $t('files.extract') }}</DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenuPortal>
            </DropdownMenuRoot>
            <button v-if="canWrite" class="btn btn-danger" :disabled="hasProtected" :title="hasProtected ? protectedTip : ''" @click="deleteOpen = true"><Trash2 class="h-4 w-4" />{{ $t('common.delete') }}</button>
          </template>
          <input ref="fileInput" type="file" multiple hidden @change="onFiles" />
        </div>

        <!-- One search box: filter this folder, or search everything. -->
        <!-- While rows are selected the toolbar is about actions: on narrower screens the
             search box steps aside (keeping its text) so the actions fit on one line. -->
        <div class="relative h-8 shrink-0 items-center rounded-lg border border-[var(--border)] bg-[var(--surface)] transition-[border-color,box-shadow] focus-within:border-brand-500 focus-within:shadow-[var(--ring)]"
          :class="selected.size ? 'hidden xl:flex' : 'flex'">
          <Search class="pointer-events-none ml-2.5 h-3.5 w-3.5 shrink-0 text-[var(--text-muted)]" />
          <input
            v-model="query" type="search" class="h-full w-28 min-w-0 bg-transparent px-2 text-[13px] outline-none placeholder:text-[var(--text-muted)] sm:w-40 lg:w-52"
            :placeholder="scope === 'dir' ? $t('files.filter') : $t('files.search')" :aria-label="$t('files.searchLabel')"
          />
          <div class="mr-0.5 flex rounded-md bg-[var(--surface-sunken)] p-0.5 text-[11px]" role="radiogroup" :aria-label="$t('files.searchScope')">
            <button role="radio" :aria-checked="scope === 'dir'" class="rounded px-1.5 py-0.5" :class="scope === 'dir' ? 'bg-[var(--surface)] text-[var(--text)] shadow-sm' : 'text-[var(--text-muted)]'" @click="scope = 'dir'">{{ $t('files.scopeDir') }}</button>
            <button role="radio" :aria-checked="scope === 'all'" class="rounded px-1.5 py-0.5" :class="scope === 'all' ? 'bg-[var(--surface)] text-[var(--text)] shadow-sm' : 'text-[var(--text-muted)]'" @click="scope = 'all'">{{ $t('files.scopeAll') }}</button>
          </div>
        </div>
        <div class="flex h-8 shrink-0 items-center rounded-lg border border-[var(--border)] bg-[var(--surface)] p-0.5" role="group" :aria-label="$t('files.view')">
          <button class="flex h-full items-center rounded-md px-2" :class="viewMode === 'list' ? 'bg-[var(--surface-sunken)] text-brand-600' : 'text-[var(--text-muted)] hover:text-[var(--text)]'" :title="$t('files.listView')" :aria-label="$t('files.listView')" @click="viewMode = 'list'"><List class="h-4 w-4" /></button>
          <button class="flex h-full items-center rounded-md px-2" :class="viewMode === 'grid' ? 'bg-[var(--surface-sunken)] text-brand-600' : 'text-[var(--text-muted)] hover:text-[var(--text)]'" :title="$t('files.gridView')" :aria-label="$t('files.gridView')" @click="viewMode = 'grid'"><LayoutGrid class="h-4 w-4" /></button>
        </div>
      </div>

      <GalleryView v-if="viewMode === 'grid' && entries.length" :entries="entries" :selected="selected" class="min-h-0 flex-1" @open="open" @select="clickRow" />
      <div v-if="viewMode === 'list'" class="col-header muted grid h-9 shrink-0 select-none items-center gap-2 border-b border-[var(--border)] bg-[var(--surface-muted)] px-3 text-xs" :style="{ gridTemplateColumns: gridCols }">
        <input type="checkbox" :checked="allSelected" :aria-label="$t('files.selectAll')" @change="toggleAll" />
        <button class="flex min-w-0 items-center justify-center gap-1 hover:text-[var(--text)]" @click="setSort('name')">
          <span class="truncate">{{ $t('files.col.name') }}</span><SortIcon col="name" />
        </button>
        <div class="relative flex min-w-0 justify-center">
          <span class="col-resizer" :title="$t('files.resizeCol')" @pointerdown="startResize($event, 'size')" @dblclick="resetCol('size')" />
          <button class="flex min-w-0 items-center gap-1 hover:text-[var(--text)]" @click="setSort('size')"><span class="truncate">{{ $t('files.col.size') }}</span><SortIcon col="size" /></button>
        </div>
        <div v-if="wide" class="relative flex min-w-0 justify-center">
          <span class="col-resizer" :title="$t('files.resizeCol')" @pointerdown="startResize($event, 'modified')" @dblclick="resetCol('modified')" />
          <button class="flex min-w-0 items-center gap-1 hover:text-[var(--text)]" @click="setSort('modTime')"><span class="truncate">{{ $t('files.col.modified') }}</span><SortIcon col="modTime" /></button>
        </div>
      </div>
      <div v-if="searchHits" class="absolute inset-0 z-10 overflow-auto bg-[var(--surface)]">
        <p class="muted border-b border-[var(--border)] px-3 py-2 text-xs">
          {{ searching ? $t('files.searching') : $t('files.found', { n: searchHits.length }) }}{{ searchHits.length >= 200 ? $t('files.truncated') : '' }}
        </p>
        <button
          v-for="h in searchHits" :key="h.path"
          class="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-[var(--surface-muted)]" @click="openHit(h)"
        >
          <FileIcon :entry="h" />
          <span class="min-w-0 flex-1">
            <span class="block truncate">{{ h.name }}</span>
            <span class="muted block truncate text-xs">{{ parentPath(h.path) }}</span>
          </span>
          <span class="muted text-xs tabular-nums">{{ h.isDir ? '' : formatSize(h.size) }}</span>
        </button>
      </div>
      <div v-show="viewMode === 'list' || !entries.length" ref="scroller" class="min-h-0 flex-1 overflow-auto">
        <div v-if="!loading && !entries.length" class="muted flex h-full flex-col items-center justify-center gap-2 p-10 text-sm">
          <FolderOpen class="h-10 w-10 opacity-40" :stroke-width="1.25" />
          {{ filter ? $t('files.noMatch') : canWrite ? $t('files.emptyDrop') : $t('files.empty') }}
        </div>
        <div v-else :style="{ height: `${virtualizer.getTotalSize()}px`, position: 'relative' }">
          <div
            v-for="r in rows" :key="entries[r.index].path"
            class="absolute left-0 top-0 grid w-full cursor-default select-none items-center gap-2 px-3 text-sm"
            :class="selected.has(entries[r.index].path) ? 'bg-brand-50' : 'hover:bg-[var(--surface-muted)]'"
            :style="{ height: `${r.size}px`, transform: `translateY(${r.start}px)`, gridTemplateColumns: gridCols }"
            @click="clickRow(entries[r.index], $event)" @dblclick="open(entries[r.index])"
          >
            <input type="checkbox" :checked="selected.has(entries[r.index].path)" :aria-label="$t('files.selectItem', { name: entries[r.index].name })" @click.stop="toggle(entries[r.index])" />
            <span class="flex min-w-0 items-center gap-2">
              <img
                v-if="entries[r.index].thumb" :src="`/api/files/thumb?size=128&path=${encodeURIComponent(entries[r.index].path)}`"
                loading="lazy" decoding="async" alt="" class="h-7 w-7 shrink-0 rounded object-cover"
              />
              <FileIcon v-else :entry="entries[r.index]" :size="20" />
              <span class="truncate" :title="entries[r.index].name">{{ entries[r.index].name }}</span>
            </span>
            <span class="muted truncate text-center tabular-nums">{{ entries[r.index].isDir ? '—' : formatSize(entries[r.index].size) }}</span>
            <span v-if="wide" class="muted truncate text-center tabular-nums">{{ formatTime(entries[r.index].modTime) }}</span>
          </div>
        </div>
      </div>
      <Transition name="fade">
        <div v-if="dragging" class="pointer-events-none absolute inset-0 flex items-center justify-center border-2 border-dashed border-brand-500 bg-brand-50/90 text-brand-700">
          {{ $t('files.dropTo', { dir }) }}
        </div>
      </Transition>
    </div>

    <Modal v-model:open="mkdirOpen" :title="$t('files.newFolder')">
      <form @submit.prevent="doMkdir">
        <input v-model="mkdirName" class="input" :placeholder="$t('files.folderName')" autofocus />
        <p v-if="mkdirName && invalidName(mkdirName)" class="mt-1 text-xs text-red-600">{{ $t('files.invalidName') }}</p>
      </form>
      <template #footer>
        <button class="btn" @click="mkdirOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-primary" :disabled="busy || invalidName(mkdirName)" @click="doMkdir">{{ $t('common.create') }}</button>
      </template>
    </Modal>

    <Modal v-model:open="renameOpen" :title="$t('files.rename')">
      <form @submit.prevent="doRename"><input id="rename-input" v-model="renameName" class="input" autofocus /></form>
      <template #footer>
        <button class="btn" @click="renameOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-primary" :disabled="busy || invalidName(renameName)" @click="doRename">{{ $t('common.ok') }}</button>
      </template>
    </Modal>

    <Modal v-model:open="deleteOpen" :title="$t('files.trashTitle')" :description="$t('files.trashDesc', { n: selected.size })">
      <ul class="muted max-h-40 overflow-auto text-sm">
        <li v-for="e in selectedEntries.slice(0, 20)" :key="e.path" class="flex items-center gap-2 truncate"><FileIcon :entry="e" :size="16" />{{ e.name }}</li>
        <li v-if="selectedEntries.length > 20">{{ $t('files.andMore', { n: selectedEntries.length - 20 }) }}</li>
      </ul>
      <template #footer>
        <button class="btn" @click="deleteOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-danger-solid" :disabled="busy" @click="doDelete">{{ $t('files.trashTitle') }}</button>
      </template>
    </Modal>

    <FolderPicker
      v-model:open="picker.open" :title="picker.mode === 'move' ? $t('files.moveToTitle') : $t('files.copyToTitle')" :start="dir"
      :confirm-text="picker.mode === 'move' ? $t('files.moveHere') : $t('files.copyHere')"
      :exclude="selectedEntries.filter((e) => e.isDir).map((e) => e.path)" @pick="doTransfer"
    />
    <PreviewModal :entry="preview" @close="preview = null" @download="download" />
    <PluginViewer :entry="pluginView?.e ?? null" :plugin="pluginView?.p ?? null" @close="pluginView = null" @download="download" />
    <ImageLightbox :images="images" :start="lightbox" @close="lightbox = null" @download="download" />
    <ShareDialog :entry="shareTarget" @close="shareTarget = null" />
  </section>
</template>
