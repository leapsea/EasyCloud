<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Ellipsis, FolderTree, Plus, TriangleAlert } from 'lucide-vue-next'
import { DropdownMenuContent, DropdownMenuItem, DropdownMenuPortal, DropdownMenuRoot, DropdownMenuSeparator, DropdownMenuTrigger } from 'reka-ui'
import { davUrl, webdavApi, type DavConfig } from '@/api/webdav'
import { useToast } from '@/stores/toast'
import Modal from '@/components/Modal.vue'
import FileIcon from '@/components/FileIcon.vue'
import FolderPicker from '@/features/files/FolderPicker.vue'
import { baseName, leafName, looksDir, toTree, type DataNode, type FolderNode, type Leaf, type RootNode } from './davTree'

// Creates or edits a WebDAV account. Left: the account. Right: what a client
// sees at the root, as a tree — existing data shared directly, and folders
// (only a name) that gather existing data. Nothing here touches files on disk.
const props = defineProps<{ editing: DavConfig | null }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ saved: [created: { item: DavConfig; password: string } | null] }>()
const { t } = useI18n()
const toast = useToast()

// ---- account ----
const form = ref({ name: '', username: '', password: '', slug: '', enabled: true })
const tree = ref<RootNode[]>([])
const slugTouched = ref(false)
const slugEditing = ref(false)
const busy = ref(false)
// (Re)initialise whenever the dialog opens, or the account it edits changes while open.
watch([open, () => props.editing], ([o]) => {
  if (!o) return
  const c = props.editing
  form.value = c ? { name: c.name, username: c.username, password: '', slug: c.slug, enabled: c.enabled } : { name: '', username: '', password: '', slug: '', enabled: true }
  tree.value = c ? toTree(c.dirs) : []
  slugTouched.value = !!c
  slugEditing.value = false
  renaming.value = null
  picker.value = { open: false, folder: -1, replace: null }
})
// The address follows the username until edited by hand.
watch(() => form.value.username, (u) => {
  if (!slugTouched.value) form.value.slug = u.toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 32)
})
const slugOk = computed(() => /^[a-z0-9][a-z0-9-]{0,31}$/.test(form.value.slug))
const userOk = computed(() => /^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$/.test(form.value.username))
const pwOk = computed(() => props.editing !== null || form.value.password === '' || form.value.password.length >= 8)

// ---- tree ----
const nodeName = (n: RootNode) => (n.kind === 'folder' ? n.name.trim() : leafName(n))
const nameOk = (n: string) => !!n.trim() && !n.includes('/') && !['.', '..'].includes(n.trim())
const folders = computed(() => tree.value.filter((n): n is FolderNode => n.kind === 'folder'))

function dupes(names: string[]): Set<number> {
  const seen = new Map<string, number>()
  const bad = new Set<number>()
  names.forEach((n, i) => { const k = n.toLowerCase(); if (!k) return; if (seen.has(k)) { bad.add(i); bad.add(seen.get(k)!) } else seen.set(k, i) })
  return bad
}
const rootClash = computed(() => dupes(tree.value.map(nodeName)))
const itemClash = computed(() => tree.value.map((n) => (n.kind === 'folder' ? dupes(n.items.map(leafName)) : new Set<number>())))
// One message per problem row, shown under it.
function nodeError(i: number): string {
  const n = tree.value[i]
  if (n.kind === 'folder' && !nameOk(n.name)) return t('webdav.errFolderName')
  if (rootClash.value.has(i)) return t('webdav.errDupName')
  if (n.kind === 'folder' && !n.items.length) return t('webdav.errEmptyFolder')
  return ''
}
const itemError = (i: number, j: number) => (itemClash.value[i]?.has(j) ? t('webdav.errDupName') : '')
const treeOk = computed(() => tree.value.length > 0 && tree.value.every((n, i) => !nodeError(i) && (n.kind === 'data' || n.items.every((_, j) => !itemError(i, j)))))
const canSave = computed(() => !!form.value.name.trim() && userOk.value && slugOk.value && pwOk.value && treeOk.value && !busy.value)

// Inline renaming: "i" for a root node, "i/j" for an item in folder i.
const renaming = ref<string | null>(null)
const renameValue = ref('')
async function startRename(key: string) {
  const [i, j] = key.split('/').map(Number)
  const n = tree.value[i]
  renameValue.value = j !== undefined && n.kind === 'folder' ? leafName(n.items[j]) : nodeName(n)
  renaming.value = key
  await nextTick()
  const el = document.getElementById('dav-rename') as HTMLInputElement | null
  el?.focus()
  el?.select()
}
function commitRename() {
  const key = renaming.value
  if (key === null) return
  renaming.value = null
  const [i, j] = key.split('/').map(Number)
  const n = tree.value[i]
  const v = renameValue.value.trim()
  if (!n) return
  if (n.kind === 'folder' && j === undefined) { n.name = v; return }
  const leaf = j !== undefined && n.kind === 'folder' ? n.items[j] : (n as DataNode)
  leaf.alias = v === baseName(leaf.path) ? '' : v // the original name needs no alias
}

function createFolder() {
  let name = t('webdav.newFolderName')
  for (let k = 2; tree.value.some((n) => nodeName(n).toLowerCase() === name.toLowerCase()); k++) name = `${t('webdav.newFolderName')} ${k}`
  tree.value.push({ kind: 'folder', name, items: [] })
  startRename(String(tree.value.length - 1))
}
function removeNode(i: number) { tree.value.splice(i, 1) }
function removeItem(i: number, j: number) { (tree.value[i] as FolderNode).items.splice(j, 1) }
function togglePerm(n: DataNode) { n.perm = n.perm === 'rw' ? 'r' : 'rw' }

// Moving data between the root and folders.
function moveToFolder(from: { i: number; j?: number }, target: FolderNode) {
  const src = tree.value[from.i]
  const leaf: Leaf = from.j !== undefined && src.kind === 'folder' ? src.items[from.j] : (src as DataNode)
  if (target.items.some((l) => l.path === leaf.path)) { toast.info(t('toast.davDirExists'), leaf.path); return }
  target.items.push({ path: leaf.path, alias: leaf.alias, isDir: leaf.isDir })
  if (from.j !== undefined && src.kind === 'folder') src.items.splice(from.j, 1)
  else tree.value.splice(from.i, 1)
}
function moveToRoot(i: number, j: number) {
  const f = tree.value[i] as FolderNode
  const leaf = f.items[j]
  if (tree.value.some((n) => n.kind === 'data' && n.path === leaf.path)) { toast.info(t('toast.davDirExists'), leaf.path); return }
  f.items.splice(j, 1)
  tree.value.push({ kind: 'data', path: leaf.path, alias: leaf.alias, isDir: leaf.isDir, perm: 'r' })
}

// One picker: "add data" (several at once, at the root or into a folder) and
// "change source" (one). Existing files and folders only.
const picker = ref<{ open: boolean; folder: number; replace: { i: number; j?: number } | null }>({ open: false, folder: -1, replace: null })
function addData(folder = -1) { picker.value = { open: true, folder, replace: null } }
function changeSource(i: number, j?: number) { picker.value = { open: true, folder: -1, replace: { i, j } } }
const pickerStart = computed(() => {
  const { folder, replace } = picker.value
  let p: string | undefined
  // Indices may be stale (the tree was edited or replaced): read defensively.
  if (replace) { const n = tree.value[replace.i]; p = n?.kind === 'folder' ? (replace.j !== undefined ? n.items[replace.j]?.path : undefined) : n?.path }
  else if (folder >= 0) { const n = tree.value[folder]; p = n?.kind === 'folder' ? n.items.at(-1)?.path : undefined }
  else p = [...tree.value].reverse().find((n): n is DataNode => n.kind === 'data')?.path
  return p ? p.slice(0, p.lastIndexOf('/')) || '/' : '/'
})
function onPickMany(paths: string[]) {
  const f = picker.value.folder >= 0 ? (tree.value[picker.value.folder] as FolderNode) : null
  let skipped = 0
  for (const path of paths) {
    if (f) { if (f.items.some((l) => l.path === path)) skipped++; else f.items.push({ path, alias: '' }) }
    else if (tree.value.some((n) => n.kind === 'data' && n.path === path)) skipped++
    else tree.value.push({ kind: 'data', path, alias: '', perm: 'r' })
  }
  if (skipped) toast.info(t('webdav.skippedDupes', { n: skipped }))
}
function onPickOne(path: string) {
  const r = picker.value.replace
  if (!r) return
  const n = tree.value[r.i]
  const leaf = r.j !== undefined && n.kind === 'folder' ? n.items[r.j] : (n as DataNode)
  leaf.path = path
  leaf.isDir = undefined
}

// ---- save ----
async function save() {
  if (!canSave.value) return
  busy.value = true
  const body = {
    name: form.value.name.trim(), slug: form.value.slug, username: form.value.username, enabled: form.value.enabled,
    password: form.value.password || undefined,
    dirs: tree.value.flatMap((n) => n.kind === 'data'
      ? [{ path: n.path, alias: n.alias.trim(), perm: looksDir(n) ? n.perm : ('r' as const) }]
      : n.items.map((l) => ({ path: l.path, alias: l.alias.trim(), perm: 'r' as const, folder: n.name.trim() }))),
  }
  try {
    if (props.editing) { await webdavApi.update(props.editing.id, body); emit('saved', null) }
    else emit('saved', await webdavApi.create(body))
    open.value = false
  } catch (e) { toast.error(e) } finally { busy.value = false }
}

const menuItem = 'flex cursor-default items-center gap-2 rounded-md px-2.5 py-1.5 text-[13px] outline-none data-[disabled]:opacity-40 data-[highlighted]:bg-[var(--surface-muted)]'
const rowBtn = 'flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--surface-sunken)] hover:text-[var(--text)] data-[state=open]:bg-[var(--surface-sunken)]'
</script>

<template>
  <Modal v-model:open="open" :title="editing ? $t('webdav.editTitle', { name: editing.name }) : $t('webdav.new')" large>
    <form class="grid min-h-0 gap-6 md:grid-cols-[15rem_minmax(0,1fr)]" @submit.prevent="save">
      <!-- Account -->
      <section class="grid content-start gap-3">
        <h3 class="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">{{ $t('webdav.account') }}</h3>
        <label class="field">{{ $t('webdav.name') }}<input v-model="form.name" class="input" :placeholder="$t('webdav.namePh')" maxlength="64" /></label>
        <label class="field">{{ $t('login.username') }}
          <input v-model="form.username" class="input font-mono" autocapitalize="off" autocomplete="off" />
          <span v-if="form.username && !userOk" class="hint !text-red-600">{{ $t('webdav.usernameRule') }}</span>
        </label>
        <label v-if="!editing" class="field">{{ $t('login.password') }}
          <input v-model="form.password" type="password" class="input font-mono" autocomplete="new-password" :placeholder="$t('webdav.passwordPh')" />
          <span v-if="!pwOk" class="hint !text-red-600">{{ $t('webdav.passwordRule') }}</span>
        </label>
        <div class="field">
          <span class="flex items-center">{{ $t('webdav.url') }}
            <button v-if="!slugEditing" type="button" class="ml-auto text-xs font-normal text-brand-600 hover:underline" @click="slugEditing = true">{{ $t('webdav.changeUrl') }}</button>
          </span>
          <input v-if="slugEditing" v-model="form.slug" class="input input-sm font-mono" autocapitalize="off" autocomplete="off" @input="slugTouched = true" />
          <code class="block break-all rounded-md bg-[var(--surface-sunken)] px-2 py-1.5 text-xs font-normal">{{ davUrl(form.slug || '…') }}</code>
          <span v-if="form.slug && !slugOk" class="hint !text-red-600">{{ $t('webdav.slugRule') }}</span>
        </div>
        <label class="flex items-center gap-2 text-sm"><input v-model="form.enabled" type="checkbox" />{{ $t('webdav.enabledLabel') }}</label>
      </section>

      <!-- What clients see -->
      <section class="flex min-h-0 min-w-0 flex-col">
        <div class="mb-2 flex flex-wrap items-center gap-1.5">
          <h3 class="text-xs font-semibold uppercase tracking-wide text-[var(--text-muted)]">{{ $t('webdav.clientView') }}</h3>
          <button type="button" class="btn btn-sm ml-auto" @click="createFolder"><FolderTree class="h-3.5 w-3.5" />{{ $t('webdav.createFolder') }}</button>
          <button type="button" class="btn btn-primary btn-sm" @click="addData()"><Plus class="h-3.5 w-3.5" />{{ $t('webdav.addData') }}</button>
        </div>
        <div class="min-h-56 flex-1 overflow-auto rounded-xl border border-[var(--border)] py-1.5">
          <div v-if="!tree.length" class="flex h-full min-h-52 flex-col items-center justify-center gap-3 p-6 text-center">
            <p class="muted max-w-xs text-[13px] leading-5">{{ $t('webdav.treeEmpty') }}</p>
            <div class="flex gap-2">
              <button type="button" class="btn btn-sm" @click="createFolder"><FolderTree class="h-3.5 w-3.5" />{{ $t('webdav.createFolder') }}</button>
              <button type="button" class="btn btn-primary btn-sm" @click="addData()"><Plus class="h-3.5 w-3.5" />{{ $t('webdav.addData') }}</button>
            </div>
          </div>
          <template v-for="(n, i) in tree" :key="i">
            <!-- A folder: only a name, gathering existing data. -->
            <div v-if="n.kind === 'folder'" class="pb-1">
              <div class="group flex h-9 items-center gap-2 px-3 hover:bg-[var(--surface-muted)]">
                <FolderTree class="h-4 w-4 shrink-0 text-brand-600" />
                <input v-if="renaming === String(i)" id="dav-rename" v-model="renameValue" class="input input-sm h-7 min-w-0 flex-1" maxlength="255" @keydown.enter.prevent="commitRename" @keydown.esc.stop.prevent="renaming = null" @blur="commitRename" />
                <button v-else type="button" class="min-w-0 truncate text-left text-[13px] font-medium hover:underline" :title="$t('webdav.rename')" @click="startRename(String(i))">{{ n.name || $t('webdav.unnamed') }}</button>
                <span class="muted shrink-0 text-xs">{{ $t('webdav.folderMeta', { n: n.items.length }) }}</span>
                <span class="ml-auto" />
                <DropdownMenuRoot :modal="false">
                  <DropdownMenuTrigger :class="rowBtn" :aria-label="$t('webdav.more')"><Ellipsis class="h-4 w-4" /></DropdownMenuTrigger>
                  <DropdownMenuPortal>
                    <DropdownMenuContent align="end" :side-offset="4" class="z-[80] min-w-40 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-1 shadow-[var(--shadow-pop)]">
                      <DropdownMenuItem :class="menuItem" @select="addData(i)">{{ $t('webdav.addData') }}</DropdownMenuItem>
                      <DropdownMenuItem :class="menuItem" @select="startRename(String(i))">{{ $t('webdav.rename') }}</DropdownMenuItem>
                      <DropdownMenuSeparator class="my-1 h-px bg-[var(--border)]" />
                      <DropdownMenuItem :class="[menuItem, 'text-red-600']" @select="removeNode(i)">{{ $t('webdav.remove') }}</DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenuPortal>
                </DropdownMenuRoot>
              </div>
              <div v-for="(l, j) in n.items" :key="l.path" class="ml-5 border-l border-[var(--border)]">
                <div class="group flex h-8 items-center gap-2 pl-4 pr-3 hover:bg-[var(--surface-muted)]">
                  <FileIcon :entry="{ isDir: looksDir(l), name: l.path }" :size="16" />
                  <input v-if="renaming === `${i}/${j}`" id="dav-rename" v-model="renameValue" class="input input-sm h-7 w-40" maxlength="255" @keydown.enter.prevent="commitRename" @keydown.esc.stop.prevent="renaming = null" @blur="commitRename" />
                  <button v-else type="button" class="shrink-0 text-[13px] hover:underline" :title="$t('webdav.rename')" @click="startRename(`${i}/${j}`)">{{ leafName(l) }}</button>
                  <span class="muted min-w-0 flex-1 truncate font-mono text-xs" :title="l.path">{{ l.path }}</span>
                  <DropdownMenuRoot :modal="false">
                    <DropdownMenuTrigger :class="rowBtn" :aria-label="$t('webdav.more')"><Ellipsis class="h-4 w-4" /></DropdownMenuTrigger>
                    <DropdownMenuPortal>
                      <DropdownMenuContent align="end" :side-offset="4" class="z-[80] min-w-44 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-1 shadow-[var(--shadow-pop)]">
                        <DropdownMenuItem :class="menuItem" @select="startRename(`${i}/${j}`)">{{ $t('webdav.rename') }}</DropdownMenuItem>
                        <DropdownMenuItem :class="menuItem" @select="changeSource(i, j)">{{ $t('webdav.changeSource') }}</DropdownMenuItem>
                        <DropdownMenuSeparator class="my-1 h-px bg-[var(--border)]" />
                        <DropdownMenuItem :class="menuItem" @select="moveToRoot(i, j)">{{ $t('webdav.moveToRoot') }}</DropdownMenuItem>
                        <DropdownMenuItem v-for="f in folders.filter((x) => x !== n)" :key="f.name" :class="menuItem" @select="moveToFolder({ i, j }, f)">{{ $t('webdav.moveTo', { name: f.name || $t('webdav.unnamed') }) }}</DropdownMenuItem>
                        <DropdownMenuSeparator class="my-1 h-px bg-[var(--border)]" />
                        <DropdownMenuItem :class="[menuItem, 'text-red-600']" @select="removeItem(i, j)">{{ $t('webdav.remove') }}</DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenuPortal>
                  </DropdownMenuRoot>
                </div>
                <p v-if="itemError(i, j)" class="flex items-center gap-1 pb-1 pl-10 text-xs text-red-600"><TriangleAlert class="h-3 w-3" />{{ itemError(i, j) }}</p>
              </div>
              <div class="ml-5 border-l border-[var(--border)]">
                <button type="button" class="flex h-8 items-center gap-1.5 pl-4 text-[13px] text-brand-600 hover:underline" @click="addData(i)"><Plus class="h-3.5 w-3.5" />{{ $t('webdav.addData') }}</button>
              </div>
              <p v-if="nodeError(i)" class="flex items-center gap-1 px-3 pb-1 text-xs text-red-600"><TriangleAlert class="h-3 w-3" />{{ nodeError(i) }}</p>
            </div>

            <!-- Existing data shared at the root. -->
            <div v-else>
              <div class="group flex h-9 items-center gap-2 px-3 hover:bg-[var(--surface-muted)]">
                <FileIcon :entry="{ isDir: looksDir(n), name: n.path }" :size="16" />
                <input v-if="renaming === String(i)" id="dav-rename" v-model="renameValue" class="input input-sm h-7 w-40" maxlength="255" @keydown.enter.prevent="commitRename" @keydown.esc.stop.prevent="renaming = null" @blur="commitRename" />
                <button v-else type="button" class="shrink-0 text-[13px] font-medium hover:underline" :title="$t('webdav.rename')" @click="startRename(String(i))">{{ leafName(n) }}</button>
                <span class="muted min-w-0 flex-1 truncate font-mono text-xs" :title="n.path">{{ n.path }}</span>
                <button
                  v-if="looksDir(n)" type="button" class="badge shrink-0 cursor-pointer" :class="n.perm === 'rw' ? 'badge-success' : 'badge-warning'"
                  :title="$t('webdav.togglePerm')" @click="togglePerm(n)"
                >{{ n.perm === 'rw' ? $t('grants.rw') : $t('files.readOnly') }}</button>
                <span v-else class="badge badge-warning shrink-0">{{ $t('files.readOnly') }}</span>
                <DropdownMenuRoot :modal="false">
                  <DropdownMenuTrigger :class="rowBtn" :aria-label="$t('webdav.more')"><Ellipsis class="h-4 w-4" /></DropdownMenuTrigger>
                  <DropdownMenuPortal>
                    <DropdownMenuContent align="end" :side-offset="4" class="z-[80] min-w-44 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-1 shadow-[var(--shadow-pop)]">
                      <DropdownMenuItem :class="menuItem" @select="startRename(String(i))">{{ $t('webdav.rename') }}</DropdownMenuItem>
                      <DropdownMenuItem :class="menuItem" @select="changeSource(i)">{{ $t('webdav.changeSource') }}</DropdownMenuItem>
                      <template v-if="folders.length">
                        <DropdownMenuSeparator class="my-1 h-px bg-[var(--border)]" />
                        <DropdownMenuItem v-for="f in folders" :key="f.name" :class="menuItem" @select="moveToFolder({ i }, f)">{{ $t('webdav.moveTo', { name: f.name || $t('webdav.unnamed') }) }}</DropdownMenuItem>
                      </template>
                      <DropdownMenuSeparator class="my-1 h-px bg-[var(--border)]" />
                      <DropdownMenuItem :class="[menuItem, 'text-red-600']" @select="removeNode(i)">{{ $t('webdav.remove') }}</DropdownMenuItem>
                    </DropdownMenuContent>
                  </DropdownMenuPortal>
                </DropdownMenuRoot>
              </div>
              <p v-if="nodeError(i)" class="flex items-center gap-1 px-3 pb-1 pl-9 text-xs text-red-600"><TriangleAlert class="h-3 w-3" />{{ nodeError(i) }}</p>
            </div>
          </template>
        </div>
        <p class="muted mt-2 text-xs leading-5">{{ $t('webdav.treeHint') }}</p>
      </section>
    </form>
    <template #footer>
      <button class="btn" @click="open = false">{{ $t('common.cancel') }}</button>
      <button class="btn btn-primary" :disabled="!canSave" @click="save">{{ editing ? $t('common.save') : $t('common.create') }}</button>
    </template>
  </Modal>

  <FolderPicker
    :open="picker.open && !picker.replace" :title="$t('webdav.pickData')" :start="pickerStart" files no-create multiple
    :confirm-text="$t('webdav.addConfirm')" @update:open="(v) => (picker.open = v)" @pick-many="onPickMany"
  />
  <FolderPicker
    :open="picker.open && !!picker.replace" :title="$t('webdav.changeSource')" :start="pickerStart" files no-create
    :confirm-text="$t('grants.pickConfirm')" @update:open="(v) => (picker.open = v)" @pick="onPickOne"
  />
</template>
