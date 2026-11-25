<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import ThemeToggle from '@/components/ThemeToggle.vue'
import LangToggle from '@/components/LangToggle.vue'
import { Cloud, Download, Eye, Link2Off } from 'lucide-vue-next'
import Modal from '@/components/Modal.vue'
import PluginViewer from '@/plugins/PluginViewer.vue'
import ShareMedia from './ShareMedia.vue'
import { previewKind, viewerFor, type PreviewKind } from './preview'
import type { ViewerPlugin } from '@/api/plugins'
import FileIcon from '@/components/FileIcon.vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { shareApi, type ShareMeta } from '@/api/share'
import { ApiError, errorMessage } from '@/api/client'
import type { FileEntry } from '@/api/files'
import { formatSize, formatTime } from '@/components/format'
const { t } = useI18n()

// Public page for visitors: no login, no app chrome.
const route = useRoute()
const id = String(route.params.id)
const meta = ref<ShareMeta | null>(null)
const gone = ref('')
const password = ref('')
const pwError = ref('')
const token = ref('')
const dir = ref('/')
const entries = ref<FileEntry[]>([])
const loading = ref(false)

const msg = (e: unknown) => (e instanceof ApiError && e.code === 'NOT_FOUND' ? t('publicShare.notFound') : errorMessage(e))

async function loadMeta() {
  try { meta.value = await shareApi.meta(id); if (meta.value.unlocked && meta.value.isDir) await openDir('/') } catch (e) { gone.value = msg(e) }
}
async function unlock() {
  pwError.value = ''
  try {
    token.value = (await shareApi.unlock(id, password.value.trim())).token
    await loadMeta()
  } catch (e) { pwError.value = msg(e) }
}
async function openDir(p: string) {
  loading.value = true
  try { const l = await shareApi.list(id, p); dir.value = l.path; entries.value = l.entries } catch (e) { gone.value = msg(e) } finally { loading.value = false }
}
const crumbs = computed(() => {
  const segs = dir.value.split('/').filter(Boolean)
  return [{ name: meta.value?.name ?? '', path: '/' }, ...segs.map((s, i) => ({ name: s, path: '/' + segs.slice(0, i + 1).join('/') }))]
})
const gallery = computed(() => entries.value.filter((e) => e.thumb).length >= Math.max(1, entries.value.length / 2))

// Online preview (shares without a download limit only): built-in players and
// viewers, or an installed viewer plugin. Files with neither (Office, or a
// type whose plugin is missing) have no preview action; they are downloaded.
const kindOf = (name: string, mime?: string): PreviewKind | null =>
  meta.value?.preview ? previewKind(meta.value.viewers, name, mime) : null
const previewUrl = (path: string) => shareApi.previewUrl(id, path, token.value)
const preview = ref<{ entry: FileEntry; kind: Exclude<PreviewKind, 'plugin'> } | null>(null)
const pluginView = ref<{ e: FileEntry; p: ViewerPlugin } | null>(null)
function openFile(e: FileEntry) {
  const k = kindOf(e.name, e.mime)
  if (!k) return
  if (k === 'plugin') { pluginView.value = { e, p: viewerFor(meta.value?.viewers, e.name, e.mime)! }; return }
  preview.value = { entry: e, kind: k }
}
const previewOpen = computed({ get: () => !!preview.value, set: (v) => { if (!v) preview.value = null } })
const resolveUrl = (p: string) => shareApi.previewToken(id, p)
function downloadEntry(e: FileEntry) { location.href = shareApi.downloadUrl(id, meta.value?.isDir ? e.path : '/', token.value) }
// A single shared file: its preview is shown right on the page.
const single = computed<FileEntry | null>(() => meta.value && !meta.value.isDir
  ? { name: meta.value.name, path: '/', isDir: false, size: meta.value.size ?? 0, mime: meta.value.mime, modTime: '' } as FileEntry
  : null)
const singleKind = computed(() => (single.value ? kindOf(single.value.name, single.value.mime) : null))
onMounted(loadMeta)
</script>

<template>
  <div class="min-h-screen px-4 py-8">
    <div class="mx-auto max-w-4xl">
      <div class="mb-6 flex items-center gap-2 text-lg font-semibold"><Cloud class="h-6 w-6 text-brand-600" fill="currentColor" :stroke-width="1.5" /> {{ $t('publicShare.brand') }}<span class="ml-auto flex gap-1.5"><LangToggle /><ThemeToggle /></span></div>

      <div v-if="gone" class="card p-10 text-center"><Link2Off class="mx-auto h-10 w-10 text-[var(--text-muted)]" :stroke-width="1.25" /><p class="mt-3">{{ gone }}</p></div>

      <div v-else-if="meta && !meta.unlocked" class="card mx-auto max-w-sm p-6">
        <p class="font-medium">{{ $t('publicShare.sharedBy', { owner: meta.owner, name: meta.name }) }}</p>
        <p class="muted mt-1 text-sm">{{ $t('publicShare.enterCode') }}</p>
        <form class="mt-4 flex gap-2" @submit.prevent="unlock">
          <input v-model="password" class="input font-mono" autofocus autocomplete="off" />
          <button class="btn btn-primary" :disabled="!password.trim()">{{ $t('publicShare.unlock') }}</button>
        </form>
        <p v-if="pwError" class="mt-2 text-sm text-red-600">{{ pwError }}</p>
      </div>

      <template v-else-if="meta">
        <div class="card flex flex-wrap items-center gap-3 p-4">
          <FileIcon :entry="{ isDir: meta.isDir, mime: meta.mime, name: meta.name }" :size="36" />
          <div class="min-w-0 flex-1">
            <p class="truncate font-medium">{{ meta.name }}</p>
            <p class="muted text-xs">
              {{ $t('publicShare.from', { owner: meta.owner }) }}{{ meta.size !== undefined && !meta.isDir ? ' · ' + formatSize(meta.size) : '' }}
              {{ meta.expireAt ? ' · ' + $t('publicShare.expires', { time: formatTime(meta.expireAt) }) : '' }}
              {{ meta.downloadsLeft >= 0 ? ' · ' + $t('publicShare.left', { n: meta.downloadsLeft }) : '' }}
            </p>
          </div>
          <button v-if="single && singleKind === 'plugin'" class="btn" @click="openFile(single)"><Eye class="h-4 w-4" />{{ $t('publicShare.preview') }}</button>
          <a v-if="!meta.isDir" class="btn btn-primary" :href="shareApi.downloadUrl(id, '/', token)"><Download class="h-4 w-4" />{{ $t('common.download') }}</a>
          <a v-else class="btn btn-primary" :href="shareApi.zipUrl(id, dir, token)"><Download class="h-4 w-4" />{{ dir !== '/' ? $t('publicShare.zipFolder') : $t('files.downloadZip') }}</a>
        </div>

        <div v-if="single && singleKind && singleKind !== 'plugin'" class="mt-4" :class="singleKind === 'audio' ? '' : 'card p-2'">
          <ShareMedia :kind="singleKind" :url="previewUrl('/')" :name="single.name" :size="single.size" :mime="single.mime" />
        </div>

        <template v-if="meta.isDir">
          <nav class="mt-4 flex flex-wrap items-center gap-1 text-sm">
            <template v-for="(c, i) in crumbs" :key="c.path">
              <span v-if="i" class="muted">/</span>
              <button class="rounded px-1.5 py-0.5 hover:bg-[var(--border)]" @click="openDir(c.path)">{{ c.name }}</button>
            </template>
          </nav>
          <div v-if="gallery" class="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-3 md:grid-cols-4">
            <template v-for="e in entries" :key="e.path">
              <button v-if="e.isDir" class="card flex aspect-square flex-col items-center justify-center gap-1 text-sm" @click="openDir(e.path)"><FileIcon :entry="e" :size="40" />{{ e.name }}</button>
              <button v-else-if="kindOf(e.name, e.mime)" class="card group overflow-hidden text-left" @click="openFile(e)">
                <img v-if="e.thumb" :src="shareApi.thumbUrl(id, e.path)" :alt="e.name" loading="lazy" class="aspect-square w-full object-cover" />
                <span v-else class="flex aspect-square items-center justify-center text-4xl"><FileIcon :entry="e" :size="40" /></span>
                <p class="truncate px-2 py-1 text-xs">{{ e.name }}</p>
              </button>
              <a v-else class="card group overflow-hidden" :href="shareApi.downloadUrl(id, e.path, token) + '&inline=1'" target="_blank" rel="noopener">
                <img v-if="e.thumb" :src="shareApi.thumbUrl(id, e.path)" :alt="e.name" loading="lazy" class="aspect-square w-full object-cover" />
                <span v-else class="flex aspect-square items-center justify-center text-4xl"><FileIcon :entry="e" :size="40" /></span>
                <p class="truncate px-2 py-1 text-xs">{{ e.name }}</p>
              </a>
            </template>
          </div>
          <div v-else class="card mt-3">
            <p v-if="!loading && !entries.length" class="muted p-8 text-center text-sm">{{ $t('files.empty') }}</p>
            <div v-for="e in entries" :key="e.path" class="flex items-center gap-3 border-b border-[var(--border)] px-4 py-2.5 text-sm last:border-0">
              <FileIcon :entry="e" />
              <button v-if="e.isDir" class="min-w-0 flex-1 truncate text-left hover:underline" @click="openDir(e.path)">{{ e.name }}</button>
              <button v-else-if="kindOf(e.name, e.mime)" class="min-w-0 flex-1 truncate text-left hover:underline" @click="openFile(e)">{{ e.name }}</button>
              <span v-else class="min-w-0 flex-1 truncate">{{ e.name }}</span>
              <span class="muted text-xs tabular-nums">{{ e.isDir ? '' : formatSize(e.size) }}</span>
              <button v-if="!e.isDir && kindOf(e.name, e.mime)" class="btn btn-sm" @click="openFile(e)"><Eye class="h-3.5 w-3.5" />{{ $t('publicShare.preview') }}</button>
              <a v-if="!e.isDir" class="btn btn-sm" :href="shareApi.downloadUrl(id, e.path, token)">{{ $t('common.download') }}</a>
            </div>
          </div>
        </template>
      </template>
    </div>
    <Modal v-model:open="previewOpen" :title="preview?.entry.name ?? ''" wide>
      <ShareMedia v-if="preview" :key="preview.entry.path" :kind="preview.kind" :url="previewUrl(preview.entry.path)" :name="preview.entry.name" :size="preview.entry.size" :mime="preview.entry.mime" />
      <template #footer>
        <a v-if="preview" class="btn" :href="shareApi.downloadUrl(id, preview.entry.path, token)"><Download class="h-4 w-4" />{{ $t('common.download') }}</a>
        <button class="btn btn-primary" @click="preview = null">{{ $t('common.close') }}</button>
      </template>
    </Modal>
    <PluginViewer :entry="pluginView?.e ?? null" :plugin="pluginView?.p ?? null" :resolve-url="resolveUrl" @close="pluginView = null" @download="downloadEntry" />
  </div>
</template>
