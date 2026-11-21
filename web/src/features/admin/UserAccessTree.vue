<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { ChevronRight, Folder, FolderOpen, ShieldCheck } from 'lucide-vue-next'
import { adminApi, type AccessEntry } from '@/api/admin'
import { errorMessage } from '@/api/client'

// The folders a user can reach, as a tree: each granted folder shows its
// effective permission and where it comes from (own grant or a department);
// folders above grants are shown only as the way there.
const props = defineProps<{ userId: string }>()
const { t } = useI18n()

interface Node {
  name: string
  path: string
  children: Node[]
  grants: AccessEntry[]
  eff: '' | 'r' | 'rw'   // effective permission (own grants + inherited from above)
  inherited: boolean      // eff comes from an ancestor rather than this folder's grants
  exists: boolean
}

const loading = ref(true)
const error = ref('')
const admin = ref(false)
const entries = ref<AccessEntry[]>([])
const collapsed = ref(new Set<string>())

watch(() => props.userId, async (id) => {
  loading.value = true; error.value = ''; entries.value = []; collapsed.value = new Set()
  try {
    const r = await adminApi.userAccess(id)
    admin.value = r.admin
    entries.value = r.items
  } catch (e) { error.value = errorMessage(e) } finally { loading.value = false }
}, { immediate: true })

const rank = (p: string) => (p === 'rw' ? 2 : p === 'r' ? 1 : 0)
const best = (a: Node['eff'], b: Node['eff']) => (rank(a) >= rank(b) ? a : b)

const tree = computed(() => {
  const root: Node = { name: '/', path: '/', children: [], grants: [], eff: '', inherited: false, exists: true }
  for (const g of entries.value) {
    let cur = root
    for (const seg of g.path.split('/').filter(Boolean)) {
      let next = cur.children.find((c) => c.name === seg)
      if (!next) {
        next = { name: seg, path: (cur.path === '/' ? '' : cur.path) + '/' + seg, children: [], grants: [], eff: '', inherited: false, exists: true }
        cur.children.push(next)
      }
      cur = next
    }
    cur.grants.push(g)
    cur.exists = g.exists
  }
  const walk = (n: Node, above: Node['eff']) => {
    const own = n.grants.reduce<Node['eff']>((a, g) => best(a, g.perm), '')
    n.eff = best(above, own)
    n.inherited = rank(above) > rank(own)
    n.children.sort((a, b) => a.name.localeCompare(b.name))
    n.children.forEach((c) => walk(c, n.eff))
  }
  walk(root, '')
  return root
})

// Flattened for rendering, honouring collapsed folders.
const rows = computed(() => {
  const out: { n: Node; depth: number }[] = []
  const add = (n: Node, depth: number) => {
    out.push({ n, depth })
    if (!collapsed.value.has(n.path)) n.children.forEach((c) => add(c, depth + 1))
  }
  // A grant on the data root itself puts the root in the list.
  if (tree.value.grants.length) add(tree.value, 0)
  else tree.value.children.forEach((c) => add(c, 0))
  return out
})

function toggle(n: Node) {
  const s = new Set(collapsed.value)
  if (s.has(n.path)) s.delete(n.path)
  else s.add(n.path)
  collapsed.value = s
}
const source = (g: AccessEntry) => (g.subjectType === 'group' ? t('users.access.viaGroup', { name: g.groupName || g.subjectId }) : t('users.access.direct'))
</script>

<template>
  <div class="min-h-40 text-sm">
    <p v-if="loading" class="muted py-8 text-center">{{ $t('common.loading') }}</p>
    <p v-else-if="error" class="py-8 text-center text-red-600">{{ error }}</p>
    <template v-else>
      <p v-if="admin" class="mb-3 flex items-center gap-2 rounded-lg bg-[var(--surface-muted)] px-3 py-2 text-xs">
        <ShieldCheck class="h-4 w-4 shrink-0 text-brand-600" />{{ $t('users.access.admin') }}
      </p>
      <p v-if="!rows.length" class="muted py-8 text-center">{{ $t('users.access.none') }}</p>
      <div v-else class="max-h-[60vh] overflow-auto rounded-lg border border-[var(--border)] py-1" role="tree">
        <div
          v-for="{ n, depth } in rows" :key="n.path" role="treeitem" :aria-expanded="n.children.length ? !collapsed.has(n.path) : undefined"
          class="flex min-h-8 items-center gap-1.5 py-1 pr-3 hover:bg-[var(--surface-muted)]" :style="{ paddingLeft: `${8 + depth * 18}px` }"
          :title="n.path"
        >
          <button v-if="n.children.length" class="flex h-5 w-5 shrink-0 items-center justify-center rounded text-[var(--text-muted)] hover:text-[var(--text)]" :aria-label="n.path" @click="toggle(n)">
            <ChevronRight class="h-3.5 w-3.5 transition-transform" :class="collapsed.has(n.path) ? '' : 'rotate-90'" />
          </button>
          <span v-else class="w-5 shrink-0" />
          <component :is="n.children.length && !collapsed.has(n.path) ? FolderOpen : Folder" class="h-4 w-4 shrink-0" :class="n.eff ? 'text-amber-500' : 'text-[var(--text-muted)] opacity-60'" />
          <span class="min-w-0 truncate" :class="{ 'muted': !n.eff, 'line-through opacity-60': !n.exists }">{{ n.name }}</span>
          <span class="ml-auto flex shrink-0 flex-wrap items-center justify-end gap-1 pl-2">
            <span v-if="!n.exists" class="badge badge-warning">{{ $t('users.access.missing') }}</span>
            <span v-for="g in n.grants" :key="g.id" class="badge">{{ source(g) }}</span>
            <span v-if="n.inherited" class="badge">{{ $t('users.access.inherited') }}</span>
            <span v-if="n.eff" class="badge" :class="n.eff === 'rw' ? 'badge-success' : 'badge-brand'">{{ n.eff === 'rw' ? $t('users.access.rw') : $t('users.access.r') }}</span>
            <span v-else class="muted text-xs">{{ $t('users.access.pathOnly') }}</span>
          </span>
        </div>
      </div>
      <p v-if="rows.length" class="muted mt-2 text-xs leading-5">{{ $t('users.access.help') }}</p>
    </template>
  </div>
</template>
