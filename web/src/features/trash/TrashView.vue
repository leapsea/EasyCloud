<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import FileIcon from '@/components/FileIcon.vue'
import { onMounted, ref } from 'vue'
import { trashApi, type TrashItem } from '@/api/files'
import { useToast } from '@/stores/toast'
import Modal from '@/components/Modal.vue'
import { formatSize, formatTime } from '@/components/format'
const { t } = useI18n()

const toast = useToast()
const items = ref<TrashItem[]>([])
const loading = ref(true)
const emptyOpen = ref(false)
const purgeTarget = ref<TrashItem | null>(null)

async function load() {
  loading.value = true
  try { items.value = (await trashApi.list()).items } catch (e) { toast.error(e) } finally { loading.value = false }
}
onMounted(load)

async function restore(i: TrashItem) {
  try {
    const { path } = await trashApi.restore(i.id, 'rename')
    toast.success(t('trash.restored', { path }))
    items.value = items.value.filter((x) => x.id !== i.id)
  } catch (e) { toast.error(e) }
}
async function purge() {
  const i = purgeTarget.value
  if (!i) return
  try { await trashApi.purge(i.id); items.value = items.value.filter((x) => x.id !== i.id); toast.success(t('trash.purged')) } catch (e) { toast.error(e) }
  purgeTarget.value = null
}
async function empty() {
  try { const { purged } = await trashApi.empty(); toast.success(t('trash.emptied', { n: purged })); items.value = [] } catch (e) { toast.error(e) }
  emptyOpen.value = false
}
function daysLeft(i: TrashItem) { return Math.max(0, Math.ceil((+new Date(i.expireAt) - Date.now()) / 86_400_000)) }
const name = (p: string) => p.split('/').pop() || p
</script>

<template>
  <section class="flex h-full flex-col">
    <PageHeader :title="$t('nav.trash')" :subtitle="$t('trash.subtitle')">
      <template #actions><button class="btn btn-danger" :disabled="!items.length" @click="emptyOpen = true">{{ $t('trash.empty') }}</button></template>
    </PageHeader>
    <div class="card min-h-0 flex-1 overflow-auto">
      <p v-if="!loading && !items.length" class="muted p-10 text-center text-sm">{{ $t('trash.isEmpty') }}</p>
      <TransitionGroup name="list" tag="ul">
        <li v-for="i in items" :key="i.id" class="flex items-center gap-3 border-b border-[var(--border)] px-4 py-3 text-sm last:border-0">
          <FileIcon :entry="{ isDir: i.isDir, name: i.originPath }" :size="20" />
          <div class="min-w-0 flex-1">
            <p class="truncate font-medium">{{ name(i.originPath) }}</p>
            <p class="muted truncate text-xs">{{ $t('trash.meta', { path: i.originPath, time: formatTime(i.deletedAt), days: daysLeft(i) }) }}</p>
          </div>
          <span class="muted tabular-nums">{{ i.isDir ? '' : formatSize(i.size) }}</span>
          <button class="btn" @click="restore(i)">{{ $t('trash.restore') }}</button>
          <button class="btn btn-danger" @click="purgeTarget = i">{{ $t('trash.purge') }}</button>
        </li>
      </TransitionGroup>
    </div>
    <Modal v-model:open="emptyOpen" :title="$t('trash.empty')" :description="$t('trash.emptyDesc')">
      <template #footer>
        <button class="btn" @click="emptyOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-danger-solid" @click="empty">{{ $t('trash.purgeAll') }}</button>
      </template>
    </Modal>
    <Modal :open="!!purgeTarget" @update:open="(v) => { if (!v) purgeTarget = null }" :title="$t('trash.purge')" :description="$t('trash.purgeDesc', { name: purgeTarget ? name(purgeTarget.originPath) : '' })">
      <template #footer>
        <button class="btn" @click="purgeTarget = null">{{ $t('common.cancel') }}</button>
        <button class="btn btn-danger-solid" @click="purge">{{ $t('trash.purge') }}</button>
      </template>
    </Modal>
  </section>
</template>
