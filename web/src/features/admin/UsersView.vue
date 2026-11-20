<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import { Plus } from 'lucide-vue-next'
import { computed, nextTick, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { adminApi, type Group, type User } from '@/api/admin'
import { filesApi } from '@/api/files'
import { useToast } from '@/stores/toast'
import { useAuth } from '@/stores/auth'
import Modal from '@/components/Modal.vue'
import { formatSize, formatTime } from '@/components/format'
const { t } = useI18n()

const toast = useToast()
const auth = useAuth()
const route = useRoute()
const users = ref<User[]>([])
const groups = ref<Group[]>([])
const usage = ref<Record<string, number>>({})
const q = ref('')

const groupName = (id: string) => groups.value.find((g) => g.id === id)?.name ?? id
const filtered = computed(() => {
  const s = q.value.trim().toLowerCase()
  return users.value.filter((u) => !s || u.username.includes(s) || u.displayName.toLowerCase().includes(s))
})

async function load() {
  try {
    const [u, g] = await Promise.all([adminApi.users(), adminApi.groups()])
    users.value = u.items
    groups.value = g.items
    for (const x of u.items) {
      filesApi.usage(`/user/${x.username}`).then((r) => (usage.value = { ...usage.value, [x.id]: r.bytes })).catch(() => {})
    }
  } catch (e) { toast.error(e) }
}
onMounted(async () => {
  await load()
  const focus = route.query.focus
  if (typeof focus === 'string') {
    const u = users.value.find((x) => x.id === focus)
    if (u) { startEdit(u); await nextTick(); document.getElementById('edit-quota')?.focus() }
  }
})

// Create
const createOpen = ref(false)
const form = ref({ username: '', displayName: '', password: '', role: 'user', quotaGB: 0, groupIds: [] as string[] })
function startCreate() { form.value = { username: '', displayName: '', password: '', role: 'user', quotaGB: 0, groupIds: [] }; createOpen.value = true }
const usernameOk = computed(() => /^[a-z0-9][a-z0-9_.-]{0,31}$/.test(form.value.username))
async function create() {
  try {
    const f = form.value
    await adminApi.createUser({ username: f.username, displayName: f.displayName, password: f.password, role: f.role, quotaBytes: Math.round(f.quotaGB * 1024 ** 3), groupIds: f.groupIds })
    toast.success(t('users.created', { name: f.username }))
    createOpen.value = false
    load()
  } catch (e) { toast.error(e) }
}

// Edit
const editOpen = ref(false)
const editing = ref<User | null>(null)
const edit = ref({ displayName: '', role: 'user' as 'user' | 'admin', disabled: false, quotaGB: 0, groupIds: [] as string[] })
function startEdit(u: User) {
  editing.value = u
  edit.value = { displayName: u.displayName, role: u.role, disabled: u.disabled, quotaGB: +(u.quotaBytes / 1024 ** 3).toPrecision(6), groupIds: [...u.groupIds] }
  editOpen.value = true
}
async function saveEdit() {
  const u = editing.value!
  try {
    await adminApi.updateUser(u.id, { displayName: edit.value.displayName, role: edit.value.role, disabled: edit.value.disabled, quotaBytes: Math.round(edit.value.quotaGB * 1024 ** 3), groupIds: edit.value.groupIds })
    toast.success(t('common.saved'))
    editOpen.value = false
    load()
  } catch (e) { toast.error(e) }
}

// Password / delete
const pwOpen = ref(false), pwValue = ref('')
async function resetPw() {
  try { await adminApi.resetPassword(editing.value!.id, pwValue.value); toast.success(t('users.pwReset')); pwOpen.value = false } catch (e) { toast.error(e) }
}
// Removes the user's authenticator (lost phone); they can bind a new one.
async function resetTwoFactor() {
  const u = editing.value!
  if (!confirm(t('users.resetTwoFactorConfirm', { name: u.username }))) return
  try { await adminApi.resetTwoFactor(u.id); toast.success(t('users.twoFactorReset')); u.twoFactor = false; load() } catch (e) { toast.error(e) }
}
const delOpen = ref(false)
async function del() {
  try { await adminApi.deleteUser(editing.value!.id); toast.success(t('users.deleted')); delOpen.value = false; editOpen.value = false; load() } catch (e) { toast.error(e) }
}
function genPassword() {
  const a = new Uint8Array(12); crypto.getRandomValues(a)
  return Array.from(a, (b) => 'abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'[b % 55]).join('')
}
</script>

<template>
  <section class="flex h-full flex-col">
    <PageHeader :title="$t('nav.users')" :subtitle="$t('users.count', { n: users.length })">
      <template #actions>
        <input v-model="q" type="search" class="input input-sm !w-32 sm:!w-48" :placeholder="$t('users.search')" />
        <button class="btn btn-primary" @click="startCreate"><Plus class="h-4 w-4" />{{ $t('users.new') }}</button>
      </template>
    </PageHeader>
    <div class="card min-h-0 flex-1 overflow-auto">
      <table class="w-full text-sm">
        <thead class="muted sticky top-0 whitespace-nowrap bg-[var(--surface)] text-left text-xs">
          <tr class="border-b border-[var(--border)]">
            <th class="px-4 py-2">{{ $t('users.col.user') }}</th><th class="px-2">{{ $t('users.col.role') }}</th><th class="px-2">{{ $t('users.col.groups') }}</th><th class="px-2">{{ $t('quota.personal') }}</th><th class="hidden px-2 md:table-cell">{{ $t('users.col.created') }}</th><th />
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in filtered" :key="u.id" class="border-b border-[var(--border)] last:border-0 hover:bg-[var(--surface-muted)]" :class="{ 'opacity-50': u.disabled }">
            <td class="px-4 py-2.5">
              <p class="font-medium">{{ u.displayName }} <span v-if="u.id === auth.user?.id" class="muted text-xs">{{ $t('users.you') }}</span></p>
              <p class="muted text-xs">{{ u.username }}{{ u.disabled ? ' · ' + $t('users.disabled') : '' }}</p>
            </td>
            <td class="px-2"><span class="badge" :class="u.role === 'admin' ? 'badge-brand' : ''">{{ u.role === 'admin' ? $t('users.role.admin') : $t('users.role.user') }}</span><span v-if="u.twoFactor" class="badge badge-success ml-1" :title="$t('users.twoFactorOn')">2FA</span></td>
            <td class="px-2 text-xs">{{ u.groupIds.map(groupName).join('、') || '—' }}</td>
            <td class="px-2 text-xs tabular-nums">
              {{ usage[u.id] !== undefined ? formatSize(usage[u.id]) : '…' }}
              <template v-if="u.quotaBytes"> / {{ formatSize(u.quotaBytes) }}</template>
              <div v-if="u.quotaBytes" class="mt-1 h-1 w-24 overflow-hidden rounded bg-[var(--border)]">
                <div class="h-full" :class="(usage[u.id] ?? 0) > u.quotaBytes * 0.9 ? 'bg-red-500' : 'bg-brand-500'" :style="{ width: Math.min(100, ((usage[u.id] ?? 0) / u.quotaBytes) * 100) + '%' }" />
              </div>
            </td>
            <td class="muted hidden px-2 text-xs md:table-cell">{{ formatTime(u.createdAt) }}</td>
            <td class="px-4 text-right"><button class="btn btn-sm" @click="startEdit(u)">{{ $t('common.edit') }}</button></td>
          </tr>
        </tbody>
      </table>
    </div>

    <Modal v-model:open="createOpen" :title="$t('users.new')" :description="$t('users.newDesc')" wide>
      <form class="grid gap-4 sm:grid-cols-2" @submit.prevent="create">
        <label class="field">{{ $t('login.username') }}<input v-model="form.username" class="input" autocapitalize="off" :placeholder="$t('users.usernameHint')" /></label>
        <label class="field">{{ $t('users.displayName') }}<input v-model="form.displayName" class="input" :placeholder="$t('common.optional')" /></label>
        <label class="field sm:col-span-2">{{ $t('users.initialPassword') }}
          <div class="flex gap-2"><input v-model="form.password" class="input font-mono" /><button type="button" class="btn" @click="form.password = genPassword()">{{ $t('users.generate') }}</button></div>
        </label>
        <label class="field">{{ $t('users.col.role') }}<select v-model="form.role" class="input"><option value="user">{{ $t('users.role.user') }}</option><option value="admin">{{ $t('users.role.admin') }}</option></select></label>
        <label class="field">{{ $t('users.quota') }}<input v-model.number="form.quotaGB" type="number" min="0" step="any" class="input" /></label>
        <fieldset v-if="groups.length" class="text-sm sm:col-span-2">
          <legend>{{ $t('users.col.groups') }}</legend>
          <div class="mt-1 flex flex-wrap gap-2">
            <label v-for="g in groups" :key="g.id" class="flex items-center gap-1 rounded border border-[var(--border)] px-2 py-1"><input v-model="form.groupIds" type="checkbox" :value="g.id" />{{ g.name }}</label>
          </div>
        </fieldset>
        <p v-if="form.username && !usernameOk" class="text-xs text-red-600 sm:col-span-2">{{ $t('users.usernameRule') }}</p>
      </form>
      <template #footer>
        <button class="btn" @click="createOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-primary" :disabled="!usernameOk || form.password.length < 8" @click="create">{{ $t('common.create') }}</button>
      </template>
    </Modal>

    <Modal v-model:open="editOpen" :title="$t('users.editTitle', { name: editing?.username ?? '' })" wide>
      <form v-if="editing" class="grid gap-4 sm:grid-cols-2" @submit.prevent="saveEdit">
        <label class="field">{{ $t('users.displayName') }}<input v-model="edit.displayName" class="input" /></label>
        <label class="field">{{ $t('users.col.role') }}<select v-model="edit.role" class="input"><option value="user">{{ $t('users.role.user') }}</option><option value="admin">{{ $t('users.role.admin') }}</option></select></label>
        <label class="field">{{ $t('users.quota') }}<input id="edit-quota" v-model.number="edit.quotaGB" type="number" min="0" step="any" class="input" /></label>
        <label class="mt-6 flex items-center gap-2 text-sm"><input v-model="edit.disabled" type="checkbox" :disabled="editing.id === auth.user?.id" />{{ $t('users.disable') }}</label>
        <fieldset v-if="groups.length" class="text-sm sm:col-span-2">
          <legend>{{ $t('users.col.groups') }}</legend>
          <div class="mt-1 flex flex-wrap gap-2">
            <label v-for="g in groups" :key="g.id" class="flex items-center gap-1 rounded border border-[var(--border)] px-2 py-1"><input v-model="edit.groupIds" type="checkbox" :value="g.id" />{{ g.name }}</label>
          </div>
        </fieldset>
      </form>
      <template #footer>
        <button class="btn btn-danger mr-auto" :disabled="editing?.id === auth.user?.id" @click="delOpen = true">{{ $t('users.delete') }}</button>
        <button v-if="editing?.twoFactor" class="btn" :title="$t('users.resetTwoFactorHint')" @click="resetTwoFactor">{{ $t('users.resetTwoFactor') }}</button>
        <button class="btn" @click="pwValue = genPassword(); pwOpen = true">{{ $t('users.resetPw') }}</button>
        <button class="btn" @click="editOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-primary" @click="saveEdit">{{ $t('common.save') }}</button>
      </template>
    </Modal>

    <Modal v-model:open="pwOpen" :title="$t('users.resetPw')" :description="$t('users.resetPwDesc')">
      <input v-model="pwValue" class="input font-mono" />
      <template #footer>
        <button class="btn" @click="pwOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-primary" :disabled="pwValue.length < 8" @click="resetPw">{{ $t('users.reset') }}</button>
      </template>
    </Modal>
    <Modal v-model:open="delOpen" :title="$t('users.delete')" :description="$t('users.deleteDesc', { name: editing?.username ?? '' })">
      <template #footer>
        <button class="btn" @click="delOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-danger-solid" @click="del">{{ $t('common.delete') }}</button>
      </template>
    </Modal>
  </section>
</template>
