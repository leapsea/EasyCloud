<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import { nextTick, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { settingsApi, type SettingItem } from '@/api/files'
import { useToast } from '@/stores/toast'
import { formatSize } from '@/components/format'
const { t } = useI18n()

const route = useRoute()
const toast = useToast()
const items = ref<SettingItem[]>([])
const drafts = ref<Record<string, string>>({})

// Byte-valued settings are edited in MB; days are edited as-is.
// Byte-valued settings are edited in MB; days are edited as-is.
const meta: Record<string, { slug: string; unit: 'MB' | 'days'; mul: number }> = {
  'upload.max_file_size': { slug: 'maxFileSize', unit: 'MB', mul: 1 << 20 },
  'upload.chunk_size': { slug: 'chunkSize', unit: 'MB', mul: 1 << 20 },
  'trash.retention_days': { slug: 'trashDays', unit: 'days', mul: 1 },
  'metrics.raw_retention_days': { slug: 'rawDays', unit: 'days', mul: 1 },
  'metrics.minute_retention_days': { slug: 'minuteDays', unit: 'days', mul: 1 },
}
const label = (k: string) => (meta[k] ? t(`settings.${meta[k].slug}.label`) : k)
const help = (k: string) => (meta[k] ? t(`settings.${meta[k].slug}.help`) : '')
const unit = (k: string) => (meta[k]?.unit === 'MB' ? 'MB' : t('settings.unitDays'))

async function load() {
  try {
    items.value = (await settingsApi.list()).items
    drafts.value = Object.fromEntries(items.value.map((i) => [i.key, String(+(i.value / meta[i.key].mul).toPrecision(8))]))
  } catch (e) { toast.error(e) }
}

async function focusFromQuery() {
  const key = route.query.focus
  if (typeof key !== 'string') return
  await nextTick()
  const el = document.getElementById(`setting-${key}`) as HTMLInputElement | null
  if (!el) return
  el.scrollIntoView({ block: 'center', behavior: 'smooth' })
  el.focus()
  el.select()
  el.closest('.setting-row')?.classList.add('focus-flash')
  setTimeout(() => el.closest('.setting-row')?.classList.remove('focus-flash'), 1700)
}

onMounted(async () => { await load(); focusFromQuery() })
watch(() => route.query.focus, focusFromQuery)

async function save(i: SettingItem) {
  const v = Math.round(Number(drafts.value[i.key]) * meta[i.key].mul)
  if (!Number.isFinite(v) || v < 0) { toast.warn(t('toast.settingInvalid'), t('toast.settingInvalidD')); return }
  try { await settingsApi.set(i.key, v); toast.success(t('toast.saved'), t('toast.settingSavedD')); await load() } catch (e) { toast.error(e) }
}
async function reset(i: SettingItem) {
  try { await settingsApi.reset(i.key); toast.success(t('toast.settingReset'), t('toast.settingResetD')); await load() } catch (e) { toast.error(e) }
}
function show(i: SettingItem, v: number) { return meta[i.key].unit === 'MB' ? (v === 0 ? t('settings.unlimited') : formatSize(v)) : t('settings.nDays', { n: v }) }
</script>

<template>
  <section class="max-w-2xl">
    <PageHeader :title="$t('nav.settings')" :subtitle="$t('settings.subtitle')" />
    <div class="flex flex-col gap-3">
      <div v-for="i in items" :key="i.key" class="setting-row card p-4">
        <label :for="`setting-${i.key}`" class="font-medium">{{ label(i.key) }}</label>
        <p class="muted mt-0.5 text-xs">{{ help(i.key) }}</p>
        <form class="mt-3 flex items-center gap-2" @submit.prevent="save(i)">
          <input :id="`setting-${i.key}`" v-model="drafts[i.key]" type="number" min="0" step="any" class="input !w-40" />
          <span class="muted text-sm">{{ unit(i.key) }}</span>
          <button class="btn btn-primary ml-auto" type="submit">{{ $t('common.save') }}</button>
          <button v-if="i.override" class="btn" type="button" @click="reset(i)">{{ $t('settings.reset') }}</button>
        </form>
        <p class="muted mt-2 text-xs">
          {{ $t('settings.current', { value: show(i, i.value) }) }} · {{ i.override ? $t('settings.fromDashboard') : $t('settings.fromDeploy') }}
          <template v-if="i.override"> · {{ $t('settings.deployDefault', { value: show(i, i.default) }) }}</template>
        </p>
      </div>
    </div>
  </section>
</template>
