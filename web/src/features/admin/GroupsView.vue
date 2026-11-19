<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import { ArrowRight } from 'lucide-vue-next'
import { Building2, CornerDownRight, Plus } from 'lucide-vue-next'
import { computed, onMounted, ref } from 'vue'
import { adminApi, type Group, type User } from '@/api/admin'
import { useToast } from '@/stores/toast'
import Modal from '@/components/Modal.vue'
const { t } = useI18n()

const toast = useToast()
const groups = ref<Group[]>([])
const users = ref<User[]>([])
const selectedId = ref<string | null>(null)

async function load() {
  try {
    const [g, u] = await Promise.all([adminApi.groups(), adminApi.users()])
    groups.value = g.items
    users.value = u.items
    if (selectedId.value && !groups.value.some((x) => x.id === selectedId.value)) selectedId.value = null
  } catch (e) { toast.error(e) }
}
onMounted(load)

// Flattened tree with depth for indentation.
const tree = computed(() => {
  const byParent = new Map<string, Group[]>()
  for (const g of groups.value) {
    const k = g.parentId ?? ''
    byParent.set(k, [...(byParent.get(k) ?? []), g])
  }
  const out: { g: Group; depth: number }[] = []
  const walk = (pid: string, depth: number) => {
    for (const g of (byParent.get(pid) ?? []).sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'))) {
      out.push({ g, depth }); walk(g.id, depth + 1)
    }
  }
  walk('', 0)
  return out
})
const selected = computed(() => groups.value.find((g) => g.id === selectedId.value) ?? null)
const descendants = computed(() => {
  const s = new Set<string>()
  if (!selected.value) return s
  const add = (id: string) => { s.add(id); groups.value.filter((g) => g.parentId === id).forEach((g) => add(g.id)) }
  add(selected.value.id)
  return s
})

const newOpen = ref(false), newName = ref(''), newParent = ref('')
async function create() {
  try { const g = await adminApi.createGroup(newName.value.trim(), newParent.value || undefined); newOpen.value = false; await load(); selectedId.value = g.id } catch (e) { toast.error(e) }
}
const renameValue = ref('')
async function save(patch: { name?: string; parentId?: string; memberIds?: string[] }) {
  try { await adminApi.updateGroup(selectedId.value!, patch); toast.success(t('common.saved')); load() } catch (e) { toast.error(e) }
}
function toggleMember(uid: string) {
  const m = new Set(selected.value!.memberIds)
  m.has(uid) ? m.delete(uid) : m.add(uid)
  save({ memberIds: [...m] })
}
const delOpen = ref(false)
async function del() {
  try { await adminApi.deleteGroup(selectedId.value!); delOpen.value = false; selectedId.value = null; toast.success(t('groups.deleted')); load() } catch (e) { toast.error(e) }
}
</script>

<template>
  <section class="flex h-full flex-col">
    <PageHeader :title="$t('nav.groups')" :subtitle="$t('groups.subtitle')">
      <template #actions><button class="btn btn-primary" @click="newName = ''; newParent = selectedId ?? ''; newOpen = true"><Plus class="h-4 w-4" />{{ $t('groups.new') }}</button></template>
    </PageHeader>
    <div class="grid min-h-0 flex-1 gap-3 md:grid-cols-[280px_1fr]">
      <div class="card overflow-auto p-2">
        <p v-if="!tree.length" class="muted p-4 text-sm">{{ $t('groups.none') }}</p>
        <button
          v-for="{ g, depth } in tree" :key="g.id"
          class="flex w-full items-center gap-2 rounded-lg py-2 pr-2 text-left text-sm hover:bg-[var(--surface-muted)]"
          :class="{ '!bg-brand-50 text-brand-700': g.id === selectedId }"
          :style="{ paddingLeft: `${0.75 + depth * 1.1}rem` }" @click="selectedId = g.id; renameValue = g.name"
        >
          <CornerDownRight v-if="depth" class="h-3.5 w-3.5 shrink-0 text-[var(--text-muted)]" /><Building2 v-else class="h-4 w-4 shrink-0 text-brand-600" :stroke-width="1.75" /><span class="flex-1 truncate">{{ g.name }}</span><span class="muted text-xs">{{ g.memberIds.length }}</span>
        </button>
      </div>
      <div v-if="selected" class="card overflow-auto p-4">
        <div class="flex flex-wrap items-end gap-2">
          <label class="field">{{ $t('groups.name') }}<input v-model="renameValue" class="input !w-56" /></label>
          <button class="btn" :disabled="!renameValue.trim() || renameValue === selected.name" @click="save({ name: renameValue.trim() })">{{ $t('files.rename') }}</button>
          <label class="field">{{ $t('groups.parent') }}
            <select class="input !w-48" :value="selected.parentId ?? ''" @change="save({ parentId: ($event.target as HTMLSelectElement).value })">
              <option value="">{{ $t('groups.topLevel') }}</option>
              <option v-for="{ g, depth } in tree.filter((x) => !descendants.has(x.g.id))" :key="g.id" :value="g.id">{{ '　'.repeat(depth) + g.name }}</option>
            </select>
          </label>
          <button class="btn btn-danger ml-auto" @click="delOpen = true">{{ $t('groups.delete') }}</button>
        </div>
        <h2 class="mt-6 text-sm font-medium">{{ $t('groups.members', { n: selected.memberIds.length }) }}</h2>
        <div class="mt-2 grid gap-1 sm:grid-cols-2 lg:grid-cols-3">
          <label v-for="u in users" :key="u.id" class="flex items-center gap-2 rounded-lg border border-[var(--border)] px-3 py-2 text-sm">
            <input type="checkbox" :checked="selected.memberIds.includes(u.id)" @change="toggleMember(u.id)" />
            <span class="truncate">{{ u.displayName }}</span><span class="muted text-xs">{{ u.username }}</span>
          </label>
        </div>
        <RouterLink class="btn mt-6" :to="{ path: '/admin/grants', query: { subjectType: 'group', subjectId: selected.id } }">{{ $t('groups.manageGrants') }}<ArrowRight class="h-4 w-4" /></RouterLink>
      </div>
      <div v-else class="card muted flex items-center justify-center p-10 text-sm">{{ $t('groups.pick') }}</div>
    </div>

    <Modal v-model:open="newOpen" :title="$t('groups.new')">
      <form class="grid gap-4" @submit.prevent="create">
        <label class="field">{{ $t('groups.name') }}<input v-model="newName" class="input" autofocus /></label>
        <label class="field">{{ $t('groups.parent') }}
          <select v-model="newParent" class="input">
            <option value="">{{ $t('groups.topLevel') }}</option>
            <option v-for="{ g, depth } in tree" :key="g.id" :value="g.id">{{ '　'.repeat(depth) + g.name }}</option>
          </select>
        </label>
      </form>
      <template #footer>
        <button class="btn" @click="newOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-primary" :disabled="!newName.trim()" @click="create">{{ $t('common.create') }}</button>
      </template>
    </Modal>
    <Modal v-model:open="delOpen" :title="$t('groups.delete')" :description="$t('groups.deleteDesc', { name: selected?.name ?? '' })">
      <template #footer>
        <button class="btn" @click="delOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-danger-solid" @click="del">{{ $t('common.delete') }}</button>
      </template>
    </Modal>
  </section>
</template>
