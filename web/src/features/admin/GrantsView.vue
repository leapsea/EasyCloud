<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import { Building2, Plus, UserRound, X } from 'lucide-vue-next'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { adminApi, type Grant, type Group, type User } from '@/api/admin'
import { useToast } from '@/stores/toast'
import Modal from '@/components/Modal.vue'
import FolderPicker from '@/features/files/FolderPicker.vue'
import { formatTime } from '@/components/format'
const { t } = useI18n()

const toast = useToast()
const route = useRoute()
const router = useRouter()
const grants = ref<Grant[]>([])
const users = ref<User[]>([])
const groups = ref<Group[]>([])
const filter = computed(() => ({ type: route.query.subjectType as string | undefined, id: route.query.subjectId as string | undefined }))

async function load() {
  try {
    const [g, u, gr] = await Promise.all([adminApi.grants(filter.value.type, filter.value.id), adminApi.users(), adminApi.groups()])
    grants.value = g.items; users.value = u.items; groups.value = gr.items
  } catch (e) { toast.error(e) }
}
onMounted(load)
watch(() => route.fullPath, load)

function subjectLabel(g: { subjectType: string; subjectId: string }) {
  if (g.subjectType === 'user') {
    const u = users.value.find((x) => x.id === g.subjectId)
    return u ? `${u.displayName} (${u.username})` : t('grants.deletedUser')
  }
  return groups.value.find((x) => x.id === g.subjectId)?.name ?? t('grants.deletedGroup')
}
const filterLabel = computed(() => filter.value.type ? subjectLabel({ subjectType: filter.value.type, subjectId: filter.value.id! }) : '')

// Grants grouped by subject (users first, then departments), each group sorted by folder.
const grouped = computed(() => {
  const m = new Map<string, { key: string; type: string; label: string; items: Grant[] }>()
  for (const g of grants.value) {
    const key = `${g.subjectType}:${g.subjectId}`
    if (!m.has(key)) m.set(key, { key, type: g.subjectType, label: subjectLabel(g), items: [] })
    m.get(key)!.items.push(g)
  }
  const list = [...m.values()]
  list.forEach((x) => x.items.sort((a, b) => a.path.localeCompare(b.path)))
  return list.sort((a, b) => (a.type === b.type ? a.label.localeCompare(b.label) : a.type === 'user' ? -1 : 1))
})

const newOpen = ref(false)
const pickerOpen = ref(false)
const form = ref({ subject: '', path: '/', perm: 'rw' })
function startNew() {
  form.value = { subject: filter.value.type ? `${filter.value.type}:${filter.value.id}` : '', path: '/share', perm: 'rw' }
  newOpen.value = true
}
async function create() {
  const [subjectType, subjectId] = form.value.subject.split(':')
  try {
    await adminApi.createGrant({ subjectType, subjectId, path: form.value.path, perm: form.value.perm })
    toast.success(t('toast.grantAdded'), t('toast.grantAddedD')); newOpen.value = false; load()
  } catch (e) { toast.error(e) }
}
async function del(g: Grant) {
  try { await adminApi.deleteGrant(g.id); toast.success(t('toast.grantRevoked'), t('toast.grantRevokedD')); load() } catch (e) { toast.error(e) }
}
const isHome = (g: Grant) => g.subjectType === 'user' && g.path === `/user/${users.value.find((u) => u.id === g.subjectId)?.username}`
</script>

<template>
  <section class="flex h-full flex-col">
    <PageHeader :title="$t('nav.grants')" :subtitle="filterLabel ? '' : $t('grants.subtitle')">
      <span v-if="filterLabel" class="flex min-w-0 items-center gap-1 rounded-full bg-[var(--surface-sunken)] py-0.5 pl-2.5 pr-1 text-xs">
        <span class="truncate">{{ filterLabel }}</span>
        <button class="muted rounded-full p-0.5 hover:text-[var(--text)]" :aria-label="$t('grants.clearFilter')" @click="router.push('/admin/grants')"><X class="h-3 w-3" /></button></span>
      <template #actions><button class="btn btn-primary" @click="startNew"><Plus class="h-4 w-4" />{{ $t('grants.new') }}</button></template>
    </PageHeader>
    <div class="card min-h-0 flex-1 overflow-auto">
      <p v-if="!grants.length" class="muted p-10 text-center text-sm">{{ $t('grants.none') }}</p>
      <table v-else class="w-full text-sm">
        <thead class="muted sticky top-0 whitespace-nowrap bg-[var(--surface)] text-left text-xs">
          <tr class="border-b border-[var(--border)]"><th class="px-4 py-2">{{ $t('grants.col.folder') }}</th><th class="px-2">{{ $t('grants.col.perm') }}</th><th class="hidden px-2 md:table-cell">{{ $t('audit.col.time') }}</th><th /></tr>
        </thead>
        <tbody v-for="grp in grouped" :key="grp.key">
          <tr class="border-b border-[var(--border)] bg-[var(--surface-sunken)]">
            <td colspan="4" class="px-4 py-2 font-medium">
              <span class="inline-flex items-center gap-1.5"><Building2 v-if="grp.type === 'group'" class="h-4 w-4 text-brand-600" :stroke-width="1.75" /><UserRound v-else class="h-4 w-4 text-[var(--text-muted)]" :stroke-width="1.75" />{{ grp.label }}<span class="muted text-xs font-normal">· {{ grp.items.length }}</span></span>
            </td>
          </tr>
          <tr v-for="g in grp.items" :key="g.id" class="border-b border-[var(--border)] last:border-0">
            <td class="py-2.5 pl-9 pr-4 font-mono text-xs">
              <RouterLink class="hover:underline" :to="{ path: '/files', query: { path: g.path } }">{{ g.path }}</RouterLink>
              <span v-if="isHome(g)" class="muted ml-1 font-sans">({{ $t('quota.personal') }})</span>
            </td>
            <td class="px-2"><span class="badge" :class="g.perm === 'rw' ? 'badge-success' : 'badge-warning'">{{ g.perm === 'rw' ? $t('grants.rw') : $t('files.readOnly') }}</span></td>
            <td class="muted hidden px-2 text-xs md:table-cell">{{ formatTime(g.createdAt) }}</td>
            <td class="px-4 text-right"><button class="btn btn-danger btn-sm" @click="del(g)">{{ $t('grants.revoke') }}</button></td>
          </tr>
        </tbody>
      </table>
    </div>

    <Modal v-model:open="newOpen" :title="$t('grants.new')" wide>
      <form class="grid gap-4" @submit.prevent="create">
        <label class="field">{{ $t('grants.col.subject') }}
          <select v-model="form.subject" class="input">
            <option value="" disabled>{{ $t('grants.pickSubject') }}</option>
            <optgroup :label="$t('users.col.groups')"><option v-for="g in groups" :key="g.id" :value="`group:${g.id}`">{{ g.name }}</option></optgroup>
            <optgroup :label="$t('nav.users')"><option v-for="u in users.filter((x) => x.role !== 'admin')" :key="u.id" :value="`user:${u.id}`">{{ u.displayName }} ({{ u.username }})</option></optgroup>
          </select>
        </label>
        <label class="field">{{ $t('grants.col.folder') }}
          <div class="flex gap-2"><input v-model="form.path" class="input font-mono" /><button type="button" class="btn" @click="pickerOpen = true">{{ $t('grants.pick') }}</button></div>
        </label>
        <fieldset class="text-sm">
          <legend>{{ $t('grants.col.perm') }}</legend>
          <div class="mt-1 flex gap-4">
            <label class="flex items-center gap-1"><input v-model="form.perm" type="radio" value="rw" />{{ $t('grants.rw') }}</label>
            <label class="flex items-center gap-1"><input v-model="form.perm" type="radio" value="r" />{{ $t('files.readOnly') }}</label>
          </div>
        </fieldset>
      </form>
      <template #footer>
        <button class="btn" @click="newOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-primary" :disabled="!form.subject || !form.path" @click="create">{{ $t('grants.grant') }}</button>
      </template>
    </Modal>
    <FolderPicker v-model:open="pickerOpen" :title="$t('grants.pickTitle')" :start="form.path" :confirm-text="$t('grants.pickConfirm')" @pick="(d) => (form.path = d)" />
  </section>
</template>
