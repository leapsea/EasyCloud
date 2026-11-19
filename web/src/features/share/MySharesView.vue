<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import { Lock } from 'lucide-vue-next'
import FileIcon from '@/components/FileIcon.vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { shareApi, type Share } from '@/api/share'
import { useToast } from '@/stores/toast'
import { formatTime } from '@/components/format'
const { t } = useI18n()

const toast = useToast()
const route = useRoute()
const all = computed(() => route.path.startsWith('/admin'))
const items = ref<Share[]>([])
async function load() {
  try { items.value = (await (all.value ? shareApi.all() : shareApi.mine())).items } catch (e) { toast.error(e) }
}
onMounted(load)
function state(s: Share) {
  if (s.expireAt && new Date(s.expireAt) < new Date()) return 'expired'
  if (s.maxDownloads && s.downloads >= s.maxDownloads) return 'exhausted'
  return 'active'
}
async function copy(s: Share) {
  try { await navigator.clipboard.writeText(`${location.origin}/s/${s.id}`); toast.success(t('shares.copied')) } catch { /* ignore */ }
}
async function revoke(s: Share) {
  try { await shareApi.remove(s.id); toast.success(t('shares.revoked')); load() } catch (e) { toast.error(e) }
}
</script>

<template>
  <section class="flex h-full flex-col">
    <PageHeader :title="all ? $t('nav.allShares') : $t('nav.shares')" />
    <div class="card min-h-0 flex-1 overflow-auto">
      <p v-if="!items.length" class="muted p-10 text-center text-sm">{{ $t('shares.none') }}</p>
      <table v-else class="w-full text-sm">
        <thead class="muted sticky top-0 whitespace-nowrap bg-[var(--surface)] text-left text-xs">
          <tr class="border-b border-[var(--border)]"><th class="px-4 py-2">{{ $t('shares.col.file') }}</th><th class="px-2">{{ $t('shares.col.status') }}</th><th class="px-2">{{ $t('shares.col.downloads') }}</th><th class="hidden px-2 md:table-cell">{{ $t('shares.col.expires') }}</th><th /></tr>
        </thead>
        <tbody>
          <tr v-for="s in items" :key="s.id" class="border-b border-[var(--border)] last:border-0">
            <td class="px-4 py-2.5">
              <p class="flex items-center gap-2 truncate"><FileIcon :entry="{ isDir: s.isDir, name: s.path }" />{{ s.path.split('/').pop() }}<Lock v-if="s.hasPassword" class="h-3.5 w-3.5 text-[var(--text-muted)]" :aria-label="$t('shares.hasCode')" /></p>
              <p class="muted truncate font-mono text-xs">{{ s.path }}</p>
            </td>
            <td class="px-2"><span class="badge" :class="state(s) === 'active' && 'badge-success'">{{ $t(`shares.state.${state(s)}`) }}</span></td>
            <td class="px-2 tabular-nums">{{ s.downloads }}{{ s.maxDownloads ? ` / ${s.maxDownloads}` : '' }}</td>
            <td class="muted hidden px-2 text-xs md:table-cell">{{ s.expireAt ? formatTime(s.expireAt) : $t('shares.never') }}</td>
            <td class="whitespace-nowrap px-4 text-right">
              <button class="btn btn-sm" @click="copy(s)">{{ $t('shares.copyLink') }}</button>
              <button class="btn btn-danger btn-sm ml-1" @click="revoke(s)">{{ $t('shares.revoke') }}</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
