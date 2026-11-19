<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import { onMounted, ref } from 'vue'
import { adminApi, type AuditEntry, type User } from '@/api/admin'
import { useToast } from '@/stores/toast'
import { formatTime } from '@/components/format'
const { t } = useI18n()

const toast = useToast()
const items = ref<AuditEntry[]>([])
const users = ref<User[]>([])
const f = ref({ userId: '', action: '', path: '' })
const more = ref(true)
const loading = ref(false)

const actionKeys = ['login', 'login.failed', 'password.change', 'file.upload', 'file.download', 'file.mkdir', 'file.rename', 'file.move', 'file.copy', 'file.trash', 'trash.restore', 'trash.purge', 'trash.empty', 'user.create', 'user.update', 'user.delete', 'user.password_reset', 'group.create', 'group.update', 'group.delete', 'grant.create', 'grant.delete', 'share.create', 'share.delete', 'share.download', 'setting.update', 'plugin.install', 'plugin.remove', 'plugin.toggle', 'office.save', 'app_password.create', 'app_password.delete', 'dav.put', 'dav.delete', 'dav.mkcol', 'dav.move', 'dav.copy', 'webdav.create', 'webdav.update', 'webdav.delete', 'webdav.password_reset']
const actionLabel = (k: string) => {
  const key = `audit.action.${k.replace(/\./g, '_')}`
  const v = t(key)
  return v === key ? k : v
}

async function load(reset = true) {
  loading.value = true
  try {
    const before = reset ? undefined : items.value[items.value.length - 1]?.id
    const r = await adminApi.audit({ ...f.value, before })
    items.value = reset ? r.items : [...items.value, ...r.items]
    more.value = r.items.length === 100
  } catch (e) { toast.error(e) } finally { loading.value = false }
}
onMounted(async () => { users.value = (await adminApi.users().catch(() => ({ items: [] }))).items; load() })
</script>

<template>
  <section class="flex h-full flex-col">
    <PageHeader :title="$t('nav.audit')" />
    <div class="mb-3 flex flex-wrap items-center gap-2">
      <select v-model="f.userId" class="input !w-40 input-sm" @change="load()">
        <option value="">{{ $t('audit.allUsers') }}</option>
        <option v-for="u in users" :key="u.id" :value="u.id">{{ u.username }}</option>
      </select>
      <select v-model="f.action" class="input !w-40 input-sm" @change="load()">
        <option value="">{{ $t('audit.allActions') }}</option>
        <option v-for="k in actionKeys" :key="k" :value="k">{{ actionLabel(k) }}</option>
      </select>
      <input v-model="f.path" class="input !w-56 input-sm" :placeholder="$t('audit.pathPrefix')" @keyup.enter="load()" />
      <button class="btn btn-sm" @click="load()">{{ $t('audit.query') }}</button>
    </div>
    <div class="card min-h-0 flex-1 overflow-auto">
      <table class="w-full text-sm">
        <thead class="muted sticky top-0 whitespace-nowrap bg-[var(--surface)] text-left text-xs">
          <tr class="border-b border-[var(--border)]"><th class="px-4 py-2">{{ $t('audit.col.time') }}</th><th class="px-2">{{ $t('audit.col.user') }}</th><th class="px-2">{{ $t('audit.col.action') }}</th><th class="px-2">{{ $t('audit.col.path') }}</th><th class="px-2">{{ $t('audit.col.detail') }}</th><th class="px-2">IP</th></tr>
        </thead>
        <tbody>
          <tr v-for="e in items" :key="e.id" class="border-b border-[var(--border)] last:border-0">
            <td class="muted whitespace-nowrap px-4 py-2 text-xs tabular-nums">{{ formatTime(e.at) }}</td>
            <td class="px-2">{{ e.username || '—' }}</td>
            <td class="px-2"><span class="badge" :class="e.action === 'login.failed' && 'badge-danger'">{{ actionLabel(e.action) }}</span></td>
            <td class="max-w-xs truncate px-2 font-mono text-xs" :title="e.path">{{ e.path }}</td>
            <td class="max-w-xs truncate px-2 text-xs" :title="e.detail">{{ e.detail }}</td>
            <td class="muted px-2 text-xs">{{ e.ip }}</td>
          </tr>
        </tbody>
      </table>
      <div class="p-3 text-center">
        <button v-if="more" class="btn" :disabled="loading" @click="load(false)">{{ loading ? $t('common.loading') : $t('audit.more') }}</button>
        <p v-else-if="!items.length" class="muted text-sm">{{ $t('audit.none') }}</p>
      </div>
    </div>
  </section>
</template>
