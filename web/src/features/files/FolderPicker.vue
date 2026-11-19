<script setup lang="ts">
import { Check, ChevronRight, CornerLeftUp, Folder, FolderPlus, House, X } from 'lucide-vue-next'
import { computed, nextTick, ref, watch } from 'vue'
import Modal from '@/components/Modal.vue'
import { filesApi, type Access, type FileEntry } from '@/api/files'
import { errorMessage } from '@/api/client'
import { joinPath, parentPath } from '@/components/format'

const props = defineProps<{ title: string; start: string; confirmText: string; exclude?: string[] }>()
const emit = defineEmits<{ pick: [dir: string] }>()
const open = defineModel<boolean>('open', { required: true })

const dir = ref('/')
const access = ref<Access>('nav')
const dirs = ref<FileEntry[]>([])
const err = ref('')
const loading = ref(false)

async function load(p: string) {
  err.value = ''
  loading.value = true
  creating.value = false
  try {
    const l = await filesApi.list(p)
    dir.value = l.path
    access.value = l.access
    dirs.value = l.entries.filter((e) => e.isDir)
  } catch (e) {
    // A start path that does not exist (e.g. typed by hand): fall back to its parent.
    if (p !== '/') return load(parentPath(p))
    err.value = errorMessage(e)
  } finally { loading.value = false }
}
watch(open, (o) => { if (o) load(props.start || '/') })

const crumbs = computed(() => {
  const segs = dir.value.split('/').filter(Boolean)
  return segs.map((s, i) => ({ name: s, path: '/' + segs.slice(0, i + 1).join('/') }))
})
const isExcluded = (p: string) => props.exclude?.some((x) => p === x || p.startsWith(x + '/')) ?? false

// Create a sub-folder in place, then enter it so it can be picked right away.
const creating = ref(false)
const newName = ref('')
const createErr = ref('')
const nameInput = ref<HTMLInputElement>()
const invalidName = computed(() => !newName.value.trim() || /[/\\]/.test(newName.value) || ['.', '..'].includes(newName.value.trim()))
async function startCreate() {
  creating.value = true
  newName.value = ''
  createErr.value = ''
  await nextTick()
  nameInput.value?.focus()
}
async function create() {
  if (invalidName.value) return
  createErr.value = ''
  try {
    const e = await filesApi.mkdir(joinPath(dir.value, newName.value.trim()))
    await load(e.path)
  } catch (e) { createErr.value = errorMessage(e) }
}
</script>

<template>
  <Modal v-model:open="open" :title="title" wide>
    <div class="mb-2 flex items-center gap-2">
      <button class="btn btn-sm !px-1.5" :disabled="dir === '/'" :title="$t('picker.up')" :aria-label="$t('picker.up')" @click="load(parentPath(dir))"><CornerLeftUp class="h-3.5 w-3.5" /></button>
      <nav class="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto text-[13px]" :aria-label="$t('picker.location')">
        <button class="flex items-center rounded px-1.5 py-0.5 hover:bg-[var(--surface-muted)]" @click="load('/')"><House class="h-3.5 w-3.5" /></button>
        <template v-for="(c, i) in crumbs" :key="c.path">
          <ChevronRight class="h-3 w-3 shrink-0 text-[var(--text-muted)]" />
          <button class="shrink-0 rounded px-1.5 py-0.5 hover:bg-[var(--surface-muted)]" :class="i === crumbs.length - 1 && 'font-semibold'" @click="load(c.path)">{{ c.name }}</button>
        </template>
      </nav>
      <button v-if="access === 'rw'" class="btn btn-sm" :disabled="creating" @click="startCreate"><FolderPlus class="h-3.5 w-3.5" />{{ $t('files.newFolder') }}</button>
    </div>
    <p v-if="err" class="mb-2 text-sm text-red-600">{{ err }}</p>
    <ul class="max-h-72 overflow-auto rounded-lg border border-[var(--border)]" :class="loading && 'opacity-60'">
      <li v-if="creating" class="border-b border-[var(--border)] bg-[var(--surface-muted)] px-2 py-1.5">
        <form class="flex items-center gap-2" @submit.prevent="create">
          <FolderPlus class="ml-1 h-4 w-4 shrink-0 text-brand-600" />
          <input ref="nameInput" v-model="newName" class="input input-sm" :placeholder="$t('picker.newName')" @keydown.esc.stop="creating = false" />
          <button type="submit" class="btn btn-primary btn-sm !px-1.5" :disabled="invalidName" :title="$t('common.create')" :aria-label="$t('common.create')"><Check class="h-3.5 w-3.5" /></button>
          <button type="button" class="btn btn-sm !px-1.5" :title="$t('common.cancel')" :aria-label="$t('common.cancel')" @click="creating = false"><X class="h-3.5 w-3.5" /></button>
        </form>
        <p v-if="createErr" class="mt-1 pl-7 text-xs text-red-600">{{ createErr }}</p>
      </li>
      <li v-if="!dirs.length && !creating" class="muted px-3 py-6 text-center text-[13px]">
        {{ $t('picker.noSub') }}<template v-if="access === 'rw'">{{ $t('picker.createHere') }}<button class="text-brand-600 hover:underline" @click="startCreate">{{ $t('files.newFolder') }}</button></template>
      </li>
      <li v-for="d in dirs" :key="d.path">
        <button
          class="group flex w-full items-center gap-2 px-3 py-2 text-left text-[13px] hover:bg-[var(--surface-muted)] disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="isExcluded(d.path)" @click="load(d.path)"
        >
          <Folder class="h-4 w-4 shrink-0 text-amber-500" fill="#fcd34d" :stroke-width="1.75" />
          <span class="min-w-0 flex-1 truncate">{{ d.name }}</span>
          <ChevronRight class="h-3.5 w-3.5 text-[var(--text-muted)] opacity-0 group-hover:opacity-100" />
        </button>
      </li>
    </ul>
    <p class="muted mt-2 truncate text-xs">{{ $t('picker.willSelect') }}<span class="font-mono">{{ dir }}</span></p>
    <template #footer>
      <button class="btn" @click="open = false">{{ $t('common.cancel') }}</button>
      <button class="btn btn-primary" :disabled="isExcluded(dir) || loading" @click="emit('pick', dir); open = false">{{ confirmText }}</button>
    </template>
  </Modal>
</template>
