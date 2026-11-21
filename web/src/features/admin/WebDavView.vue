<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy, FolderPlus, HardDrive, KeyRound, Pencil, Plus, Trash2, TriangleAlert, X } from 'lucide-vue-next'
import { davUrl, webdavApi, type DavConfig, type DavDir } from '@/api/webdav'
import { useToast } from '@/stores/toast'
import Modal from '@/components/Modal.vue'
import FileIcon from '@/components/FileIcon.vue'
import FolderPicker from '@/features/files/FolderPicker.vue'

const { t } = useI18n()
const toast = useToast()
const items = ref<DavConfig[]>([])
const loading = ref(true)

async function load() {
  try { items.value = (await webdavApi.list()).items } catch (e) { toast.error(e) } finally { loading.value = false }
}
onMounted(load)

async function copy(text: string) {
  try { await navigator.clipboard.writeText(text); toast.success(t('toast.copied'), t('toast.copiedD')) } catch { toast.warn(t('toast.copyFailed'), t('toast.copyFailedD')) }
}
async function toggle(c: DavConfig) {
  try {
    const u = await webdavApi.setEnabled(c.id, !c.enabled)
    Object.assign(c, u)
    if (u.enabled) toast.success(t('toast.davEnabled'), t('toast.davEnabledD'))
    else toast.info(t('toast.davDisabled'), t('toast.davDisabledD'))
  } catch (e) { toast.error(e) }
}

// ---- editor ----
const editOpen = ref(false)
const editing = ref<DavConfig | null>(null)
const form = ref({ name: '', slug: '', username: '', password: '', enabled: true, dirs: [] as DavDir[] })
const slugTouched = ref(false)
const busy = ref(false)
const picker = ref<{ open: boolean; index: number }>({ open: false, index: -1 })

function startCreate() {
  editing.value = null
  slugTouched.value = false
  form.value = { name: '', slug: '', username: '', password: '', enabled: true, dirs: [] }
  editOpen.value = true
}
function startEdit(c: DavConfig) {
  editing.value = c
  slugTouched.value = true
  form.value = { name: c.name, slug: c.slug, username: c.username, password: '', enabled: c.enabled, dirs: c.dirs.map((d) => ({ path: d.path, alias: d.alias, perm: d.perm })) }
  editOpen.value = true
}
// Suggest a URL slug from the username until the slug is edited by hand.
function onUsername() {
  if (!slugTouched.value) form.value.slug = form.value.username.toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 32)
}
const slugOk = computed(() => /^[a-z0-9][a-z0-9-]{0,31}$/.test(form.value.slug))
const userOk = computed(() => /^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$/.test(form.value.username))
const pwOk = computed(() => editing.value !== null || form.value.password === '' || form.value.password.length >= 8)

// Folder names as clients will see them; duplicates need an alias.
const displayName = (d: DavDir) => d.alias.trim() || (d.path === '/' ? 'data' : d.path.split('/').filter(Boolean).pop() ?? '')
const clashes = computed(() => {
  const seen = new Map<string, number>()
  const bad = new Set<number>()
  form.value.dirs.forEach((d, i) => {
    const k = displayName(d).toLowerCase()
    if (seen.has(k)) { bad.add(i); bad.add(seen.get(k)!) } else seen.set(k, i)
  })
  return bad
})
const canSave = computed(() => form.value.name.trim() && slugOk.value && userOk.value && pwOk.value && form.value.dirs.length > 0 && clashes.value.size === 0 && !busy.value)

// Start where the user is most likely to continue: the folder being changed,
// or next to the last folder added.
const pickerStart = computed(() => {
  if (picker.value.index >= 0) return form.value.dirs[picker.value.index]?.path ?? '/'
  const last = form.value.dirs[form.value.dirs.length - 1]?.path
  if (!last || last === '/') return '/'
  return last.slice(0, last.lastIndexOf('/')) || '/'
})
function addDir() { picker.value = { open: true, index: -1 } }
function changeDir(i: number) { picker.value = { open: true, index: i } }
function onPick(path: string) {
  if (picker.value.index >= 0) form.value.dirs[picker.value.index].path = path
  else if (form.value.dirs.some((d) => d.path === path)) toast.info(t('toast.davDirExists'), path)
  else form.value.dirs.push({ path, alias: '', perm: 'rw' })
}

const creds = ref<{ title: string; url: string; username: string; password: string } | null>(null)
async function save() {
  busy.value = true
  const body = { name: form.value.name, slug: form.value.slug, username: form.value.username, enabled: form.value.enabled,
    dirs: form.value.dirs.map((d) => ({ path: d.path, alias: d.alias.trim(), perm: d.perm })), password: form.value.password || undefined }
  try {
    if (editing.value) {
      await webdavApi.update(editing.value.id, body)
      toast.success(t('toast.saved'), t('toast.davSavedD'))
    } else {
      const r = await webdavApi.create(body)
      creds.value = { title: t('webdav.createdTitle', { name: r.item.name }), url: davUrl(r.item.slug), username: r.item.username, password: r.password }
    }
    editOpen.value = false
    load()
  } catch (e) { toast.error(e) } finally { busy.value = false }
}

const resetTarget = ref<DavConfig | null>(null)
async function resetPassword() {
  const c = resetTarget.value!
  try {
    const { password } = await webdavApi.resetPassword(c.id)
    resetTarget.value = null
    creds.value = { title: t('webdav.resetTitle', { name: c.name }), url: davUrl(c.slug), username: c.username, password }
  } catch (e) { toast.error(e) }
}
const delTarget = ref<DavConfig | null>(null)
async function remove() {
  try { await webdavApi.remove(delTarget.value!.id); delTarget.value = null; toast.success(t('toast.davDeleted')); load() } catch (e) { toast.error(e) }
}
</script>

<template>
  <section class="flex h-full flex-col">
    <PageHeader :title="$t('nav.webdav')" :subtitle="$t('webdav.subtitle')">
      <template #actions><button class="btn btn-primary" @click="startCreate"><Plus class="h-4 w-4" />{{ $t('webdav.new') }}</button></template>
    </PageHeader>

    <div class="grid min-h-0 flex-1 content-start gap-3 overflow-auto lg:grid-cols-2">
      <div v-if="!loading && !items.length" class="card flex flex-col items-center gap-2 p-10 text-center lg:col-span-2">
        <HardDrive class="h-10 w-10 opacity-40" :stroke-width="1.25" />
        <p class="text-sm">{{ $t('webdav.none') }}</p>
        <button class="btn btn-primary mt-2" @click="startCreate"><Plus class="h-4 w-4" />{{ $t('webdav.new') }}</button>
      </div>

      <article v-for="c in items" :key="c.id" class="card flex flex-col p-4" :class="!c.enabled && 'opacity-70'">
        <header class="flex items-start gap-3">
          <div class="min-w-0 flex-1">
            <p class="truncate font-medium">{{ c.name }}</p>
            <p class="muted text-xs">{{ $t('webdav.dirCount', { n: c.dirs.length }) }}</p>
          </div>
          <button
            role="switch" :aria-checked="c.enabled" :title="c.enabled ? $t('webdav.disable') : $t('webdav.enable')"
            class="relative mt-0.5 h-5 w-9 shrink-0 rounded-full transition-colors" :class="c.enabled ? 'bg-brand-600' : 'bg-[var(--border)]'"
            @click="toggle(c)"
          ><span class="absolute top-0.5 h-4 w-4 rounded-full bg-white shadow transition-[left]" :class="c.enabled ? 'left-[18px]' : 'left-0.5'" /></button>
        </header>

        <dl class="mt-3 grid grid-cols-[minmax(4rem,max-content)_1fr] items-center gap-x-3 gap-y-1.5 text-[13px]">
          <dt class="muted">{{ $t('webdav.url') }}</dt>
          <dd class="flex min-w-0 items-center gap-1.5">
            <code class="truncate rounded bg-[var(--surface-sunken)] px-1.5 py-0.5 text-xs">{{ davUrl(c.slug) }}</code>
            <button class="btn btn-sm !px-1.5" :title="$t('common.copy')" :aria-label="$t('common.copy')" @click="copy(davUrl(c.slug))"><Copy class="h-3.5 w-3.5" /></button>
          </dd>
          <dt class="muted">{{ $t('login.username') }}</dt>
          <dd class="flex items-center gap-1.5">
            <code class="rounded bg-[var(--surface-sunken)] px-1.5 py-0.5 text-xs">{{ c.username }}</code>
            <button class="btn btn-sm !px-1.5" :title="$t('common.copy')" :aria-label="$t('common.copy')" @click="copy(c.username)"><Copy class="h-3.5 w-3.5" /></button>
          </dd>
        </dl>

        <p class="muted mt-4 text-xs font-medium">{{ $t('webdav.sharedDirs') }}</p>
        <ul class="mt-1.5 divide-y divide-[var(--border)] rounded-lg border border-[var(--border)]">
          <li v-for="d in c.dirs" :key="d.path" class="flex items-center gap-2 px-3 py-2 text-[13px]">
            <FileIcon :entry="{ isDir: true, name: d.name ?? '' }" :size="16" />
            <span class="font-medium">{{ d.name }}</span>
            <span class="muted min-w-0 flex-1 truncate font-mono text-xs" :title="d.path">{{ d.path }}</span>
            <span v-if="d.missing" class="badge badge-danger" :title="$t('webdav.missingHint')"><TriangleAlert class="h-3 w-3" />{{ $t('webdav.missing') }}</span>
            <span class="badge" :class="d.perm === 'rw' ? 'badge-success' : 'badge-warning'">{{ d.perm === 'rw' ? $t('grants.rw') : $t('files.readOnly') }}</span>
          </li>
        </ul>

        <footer class="mt-4 flex gap-2">
          <button class="btn btn-sm" @click="startEdit(c)"><Pencil class="h-3.5 w-3.5" />{{ $t('common.edit') }}</button>
          <button class="btn btn-sm" @click="resetTarget = c"><KeyRound class="h-3.5 w-3.5" />{{ $t('users.resetPw') }}</button>
          <button class="btn btn-danger btn-sm ml-auto" @click="delTarget = c"><Trash2 class="h-3.5 w-3.5" />{{ $t('common.delete') }}</button>
        </footer>
      </article>
    </div>

    <Modal v-model:open="editOpen" :title="editing ? $t('webdav.editTitle', { name: editing.name }) : $t('webdav.new')" :description="$t('webdav.editDesc')" wide>
      <form class="grid gap-4 sm:grid-cols-2" @submit.prevent="canSave && save()">
        <label class="field sm:col-span-2">{{ $t('webdav.name') }}<input v-model="form.name" class="input" :placeholder="$t('webdav.namePh')" maxlength="64" /></label>
        <label class="field">{{ $t('login.username') }}
          <input v-model="form.username" class="input font-mono" autocapitalize="off" autocomplete="off" @input="onUsername" />
          <span v-if="form.username && !userOk" class="hint !text-red-600">{{ $t('webdav.usernameRule') }}</span>
        </label>
        <label class="field">{{ $t('webdav.slug') }}
          <input v-model="form.slug" class="input font-mono" autocapitalize="off" autocomplete="off" @input="slugTouched = true" />
          <span class="hint" :class="form.slug && !slugOk && '!text-red-600'">{{ form.slug && !slugOk ? $t('webdav.slugRule') : $t('webdav.urlPreview', { url: davUrl(form.slug || '…') }) }}</span>
        </label>
        <label v-if="!editing" class="field sm:col-span-2">{{ $t('login.password') }}
          <input v-model="form.password" type="password" class="input font-mono" autocomplete="new-password" :placeholder="$t('webdav.passwordPh')" />
          <span v-if="!pwOk" class="hint !text-red-600">{{ $t('webdav.passwordRule') }}</span>
        </label>

        <div class="field sm:col-span-2">
          <div class="flex items-center">
            <span>{{ $t('webdav.sharedDirs') }}</span>
            <button type="button" class="btn btn-sm ml-auto" @click="addDir"><FolderPlus class="h-3.5 w-3.5" />{{ $t('webdav.addDir') }}</button>
          </div>
          <p v-if="!form.dirs.length" class="muted rounded-lg border border-dashed border-[var(--border)] px-3 py-5 text-center text-[13px] font-normal">{{ $t('webdav.noDirs') }}</p>
          <ul v-else class="divide-y divide-[var(--border)] rounded-lg border border-[var(--border)] font-normal">
            <li v-for="(d, i) in form.dirs" :key="i" class="grid grid-cols-[1fr_auto] gap-2 px-3 py-2.5 sm:grid-cols-[minmax(0,1fr)_8.5rem_6.5rem_auto] sm:items-center">
              <button type="button" class="flex min-w-0 items-center gap-2 text-left text-[13px] hover:text-brand-600" :title="$t('webdav.changeDir')" @click="changeDir(i)">
                <FileIcon :entry="{ isDir: true, name: d.path }" :size="16" /><span class="truncate font-mono text-xs">{{ d.path }}</span>
              </button>
              <button type="button" class="btn btn-sm !px-1.5 sm:order-last" :title="$t('webdav.removeDir')" :aria-label="$t('webdav.removeDir')" @click="form.dirs.splice(i, 1)"><X class="h-3.5 w-3.5" /></button>
              <input v-model="d.alias" class="input input-sm" :class="clashes.has(i) && '!border-red-500'" :placeholder="displayName({ ...d, alias: '' })" :title="$t('webdav.aliasHint')" maxlength="255" />
              <select v-model="d.perm" class="input input-sm">
                <option value="rw">{{ $t('grants.rw') }}</option>
                <option value="r">{{ $t('files.readOnly') }}</option>
              </select>
            </li>
          </ul>
          <span v-if="clashes.size" class="hint !text-red-600">{{ $t('webdav.clash') }}</span>
          <span v-else class="hint">{{ $t('webdav.aliasHint') }}</span>
        </div>
        <label class="flex items-center gap-2 text-sm sm:col-span-2"><input v-model="form.enabled" type="checkbox" />{{ $t('webdav.enabledLabel') }}</label>
      </form>
      <template #footer>
        <button class="btn" @click="editOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-primary" :disabled="!canSave" @click="save">{{ editing ? $t('common.save') : $t('common.create') }}</button>
      </template>
    </Modal>

    <FolderPicker
      v-model:open="picker.open" :title="$t('webdav.pickTitle')" :start="pickerStart"
      :confirm-text="$t('grants.pickConfirm')" @pick="onPick"
    />

    <Modal :open="!!creds" @update:open="(v) => { if (!v) creds = null }" :title="creds?.title ?? ''" :description="$t('webdav.credsOnce')" wide>
      <dl v-if="creds" class="grid grid-cols-[minmax(4rem,max-content)_1fr] items-center gap-x-3 gap-y-2 text-[13px]">
        <dt class="muted">{{ $t('webdav.url') }}</dt>
        <dd class="flex min-w-0 items-center gap-1.5"><code class="input input-sm flex-1 select-all truncate font-mono !leading-8">{{ creds.url }}</code><button class="btn btn-sm" @click="copy(creds.url)">{{ $t('common.copy') }}</button></dd>
        <dt class="muted">{{ $t('login.username') }}</dt>
        <dd class="flex min-w-0 items-center gap-1.5"><code class="input input-sm flex-1 select-all font-mono !leading-8">{{ creds.username }}</code><button class="btn btn-sm" @click="copy(creds.username)">{{ $t('common.copy') }}</button></dd>
        <dt class="muted">{{ $t('login.password') }}</dt>
        <dd class="flex min-w-0 items-center gap-1.5"><code class="input input-sm flex-1 select-all font-mono !leading-8">{{ creds.password }}</code><button class="btn btn-sm" @click="copy(creds.password)">{{ $t('common.copy') }}</button></dd>
      </dl>
      <template #footer><button class="btn btn-primary" @click="creds = null">{{ $t('account.saved') }}</button></template>
    </Modal>

    <Modal :open="!!resetTarget" @update:open="(v) => { if (!v) resetTarget = null }" :title="$t('users.resetPw')" :description="$t('webdav.resetDesc', { name: resetTarget?.name ?? '' })">
      <template #footer>
        <button class="btn" @click="resetTarget = null">{{ $t('common.cancel') }}</button>
        <button class="btn btn-primary" @click="resetPassword">{{ $t('users.reset') }}</button>
      </template>
    </Modal>
    <Modal :open="!!delTarget" @update:open="(v) => { if (!v) delTarget = null }" :title="$t('webdav.deleteTitle')" :description="$t('webdav.deleteDesc', { name: delTarget?.name ?? '' })">
      <template #footer>
        <button class="btn" @click="delTarget = null">{{ $t('common.cancel') }}</button>
        <button class="btn btn-danger-solid" @click="remove">{{ $t('common.delete') }}</button>
      </template>
    </Modal>
  </section>
</template>
