<script setup lang="ts">
import { copyText as clipCopy } from '@/components/clipboard'
import PageHeader from '@/components/PageHeader.vue'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Copy, Eye, FolderTree, HardDrive, KeyRound, Pencil, Plus, Trash2, TriangleAlert } from 'lucide-vue-next'
import { davUrl, webdavApi, type DavConfig } from '@/api/webdav'
import { useToast } from '@/stores/toast'
import Modal from '@/components/Modal.vue'
import FileIcon from '@/components/FileIcon.vue'
import DavEditor from './DavEditor.vue'
import { leafName, looksDir, toTree } from './davTree'

const { t } = useI18n()
const toast = useToast()
const items = ref<DavConfig[]>([])
const loading = ref(true)

async function load() {
  try { items.value = (await webdavApi.list()).items } catch (e) { toast.error(e) } finally { loading.value = false }
}
onMounted(load)

async function copy(text: string) {
  try { await clipCopy(text); toast.success(t('toast.copied'), t('toast.copiedD')) } catch { toast.warn(t('toast.copyFailed'), t('toast.copyFailedD')) }
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
function startCreate() { editing.value = null; editOpen.value = true }
function startEdit(c: DavConfig) { editing.value = c; editOpen.value = true }
function onSaved(created: { item: DavConfig; password: string } | null) {
  if (created) creds.value = { title: t('webdav.createdTitle', { name: created.item.name }), url: davUrl(created.item.slug), username: created.item.username, password: created.password }
  else toast.success(t('toast.saved'), t('toast.davSavedD'))
  load()
}

const creds = ref<{ title: string; url: string; username: string; password: string } | null>(null)
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
const viewTarget = ref<DavConfig | null>(null) // the account whose content is shown
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

        <p v-if="c.dirs.some((x) => x.missing)" class="mt-3 flex items-center gap-1.5 text-xs text-red-600">
          <TriangleAlert class="h-3.5 w-3.5 shrink-0" />{{ $t('webdav.missingCount', { n: c.dirs.filter((x) => x.missing).length }) }}
        </p>

        <!-- Pinned to the bottom: cards in a row keep their buttons aligned. -->
        <footer class="mt-auto flex flex-wrap gap-2 pt-4">
          <button class="btn btn-sm" @click="viewTarget = c"><Eye class="h-3.5 w-3.5" />{{ $t('webdav.viewContent') }}</button>
          <button class="btn btn-sm" @click="startEdit(c)"><Pencil class="h-3.5 w-3.5" />{{ $t('common.edit') }}</button>
          <button class="btn btn-sm" @click="resetTarget = c"><KeyRound class="h-3.5 w-3.5" />{{ $t('users.resetPw') }}</button>
          <button class="btn btn-danger btn-sm ml-auto" @click="delTarget = c"><Trash2 class="h-3.5 w-3.5" />{{ $t('common.delete') }}</button>
        </footer>
      </article>
    </div>

    <Modal :open="!!viewTarget" @update:open="(v) => { if (!v) viewTarget = null }" :title="$t('webdav.viewTitle', { name: viewTarget?.name ?? '' })" :description="viewTarget ? davUrl(viewTarget.slug) : ''" wide>
      <template v-if="viewTarget">
      <ul class="rounded-lg border border-[var(--border)] py-1 text-[13px]">
          <template v-for="(n, i) in toTree(viewTarget.dirs)" :key="i">
            <li v-if="n.kind === 'folder'" class="px-3 py-1">
              <div class="flex items-center gap-2">
                <FolderTree class="h-4 w-4 shrink-0 text-brand-600" />
                <span class="font-medium">{{ n.name }}</span>
                <span class="badge badge-warning ml-auto">{{ $t('files.readOnly') }}</span>
              </div>
              <div v-for="l in n.items" :key="l.path" class="ml-2 flex items-center gap-2 border-l border-[var(--border)] py-0.5 pl-4">
                <FileIcon :entry="{ isDir: looksDir(l), name: l.path }" :size="14" />
                <span>{{ leafName(l) }}</span>
                <span class="muted min-w-0 flex-1 truncate font-mono text-xs" :title="l.path">← {{ l.path }}</span>
              </div>
            </li>
            <li v-else class="flex items-center gap-2 px-3 py-1">
              <FileIcon :entry="{ isDir: looksDir(n), name: n.path }" :size="16" />
              <span class="font-medium">{{ leafName(n) }}</span>
              <span class="muted min-w-0 flex-1 truncate font-mono text-xs" :title="n.path">← {{ n.path }}</span>
              <span class="badge" :class="n.perm === 'rw' ? 'badge-success' : 'badge-warning'">{{ n.perm === 'rw' ? $t('grants.rw') : $t('files.readOnly') }}</span>
            </li>
          </template>
          <li v-for="d in viewTarget.dirs.filter((x) => x.missing)" :key="'m' + d.path" class="flex items-center gap-2 px-3 py-1 text-xs text-red-600">
            <TriangleAlert class="h-3.5 w-3.5" />{{ $t('webdav.missing') }}：<span class="font-mono">{{ d.path }}</span>
          </li>
        </ul>
      </template>
      <template #footer>
        <button class="btn" @click="viewTarget && startEdit(viewTarget); viewTarget = null"><Pencil class="h-3.5 w-3.5" />{{ $t('common.edit') }}</button>
        <button class="btn btn-primary" @click="viewTarget = null">{{ $t('common.close') }}</button>
      </template>
    </Modal>

    <DavEditor v-model:open="editOpen" :editing="editing" @saved="onSaved" />

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
