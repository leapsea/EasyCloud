<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import { BadgeCheck, ShieldAlert, Upload } from 'lucide-vue-next'
import { nextTick, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { pluginsApi, type InstalledPlugin, type RepoPlugin } from '@/api/plugins'
import { useToast } from '@/stores/toast'
import { usePlugins } from '@/stores/plugins'
import Modal from '@/components/Modal.vue'
import { formatTime } from '@/components/format'
const { t } = useI18n()

const toast = useToast()
const route = useRoute()
const store = usePlugins()
const tab = ref<'installed' | 'repo'>('installed')
const items = ref<InstalledPlugin[]>([])
const repoUrl = ref('')
const repo = ref<RepoPlugin[] | null>(null)
const repoErr = ref('')
const busy = ref<string | null>(null)

async function load() {
  try { const r = await pluginsApi.list(); items.value = r.items; repoUrl.value = r.repository } catch (e) { toast.error(e) }
  store.load(true)
}
async function loadRepo() {
  repoErr.value = ''
  try { repo.value = (await pluginsApi.repository()).items } catch (e: any) { repoErr.value = e?.detail ?? e?.message ?? t('errors.PLUGIN_REPOSITORY_UNAVAILABLE'); repo.value = [] }
}
// ?highlight=<plugin id> (e.g. from the video category's hint): show that
// plugin, in the installed list if present (to enable it), else the repository.
const highlight = ref(typeof route.query.highlight === 'string' ? route.query.highlight : '')
async function showHighlight() {
  await nextTick()
  document.getElementById(`plugin-${highlight.value}`)?.scrollIntoView({ block: 'center' })
}
onMounted(async () => {
  await load()
  if (highlight.value) {
    if (!items.value.some((p) => p.manifest.id === highlight.value)) { tab.value = 'repo'; await loadRepo() }
    await showHighlight()
    return
  }
  const want = items.value.find((p) => p.manifest.type === 'connector' && (!p.config.documentServerUrl))
  if (route.query.focus === undefined && want) startConfig(want)
})

const kind = (k: string) => t(`plugins.kind.${k}`)
const statusText = (s: string) => t(`plugins.status.${s}`)

async function toggle(p: InstalledPlugin) {
  busy.value = p.manifest.id
  try { await pluginsApi.update(p.manifest.id, { enabled: !p.enabled }); await load() } catch (e) { toast.error(e) } finally { busy.value = null }
}
const fileInput = ref<HTMLInputElement>()
async function onFile(ev: Event) {
  const f = (ev.target as HTMLInputElement).files?.[0]
  ;(ev.target as HTMLInputElement).value = ''
  if (!f) return
  busy.value = 'upload'
  try { const p = await pluginsApi.upload(f); toast.success(t('plugins.installed', { name: p.manifest.name, version: p.manifest.version })); await load() } catch (e) { toast.error(e) } finally { busy.value = null }
}
async function installRepo(r: RepoPlugin) {
  busy.value = r.id
  try { const p = await pluginsApi.installFromRepo(r.id); toast.success(t('plugins.installed', { name: p.manifest.name, version: p.manifest.version })); await load(); await loadRepo() } catch (e) { toast.error(e) } finally { busy.value = null }
}

const cfgOpen = ref(false)
const cfgTarget = ref<InstalledPlugin | null>(null)
const cfgValues = ref<Record<string, unknown>>({})
function startConfig(p: InstalledPlugin) {
  cfgTarget.value = p
  cfgValues.value = Object.fromEntries((p.manifest.config ?? []).map((f) => [f.key, p.config[f.key] ?? f.default ?? (f.type === 'bool' ? false : '')]))
  cfgOpen.value = true
}
async function saveConfig() {
  const p = cfgTarget.value!
  const out: Record<string, unknown> = {}
  for (const f of p.manifest.config ?? []) {
    const v = cfgValues.value[f.key]
    out[f.key] = f.type === 'number' ? Number(v) : v
  }
  try { await pluginsApi.update(p.manifest.id, { config: out }); toast.success(t('plugins.configSaved')); cfgOpen.value = false; load() } catch (e) { toast.error(e) }
}
const delTarget = ref<InstalledPlugin | null>(null)
async function remove() {
  const p = delTarget.value!
  try { await pluginsApi.remove(p.manifest.id); toast.success(t('plugins.removed')); delTarget.value = null; load() } catch (e) { toast.error(e) }
}
</script>

<template>
  <section class="flex h-full flex-col">
    <PageHeader :title="$t('nav.plugins')">
      <div class="flex shrink-0 rounded-lg border border-[var(--border)] bg-[var(--surface-sunken)] p-0.5 text-[13px]" role="tablist">
        <button role="tab" :aria-selected="tab === 'installed'" class="h-7 rounded-md px-3" :class="tab === 'installed' ? 'bg-[var(--surface)] text-[var(--text)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text)]'" @click="tab = 'installed'">{{ $t('plugins.tabInstalled', { n: items.length }) }}</button>
        <button role="tab" :aria-selected="tab === 'repo'" class="h-7 rounded-md px-3" :class="tab === 'repo' ? 'bg-[var(--surface)] text-[var(--text)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text)]'" @click="tab = 'repo'; repo ?? loadRepo()">{{ $t('plugins.tabRepo') }}</button>
      </div>
      <template #actions>
        <button class="btn btn-primary" :disabled="busy === 'upload'" @click="fileInput?.click()"><Upload class="h-4 w-4" />{{ $t('plugins.upload') }}</button>
        <input ref="fileInput" type="file" accept=".zip,application/zip" hidden @change="onFile" />
      </template>
    </PageHeader>

    <div v-if="tab === 'installed'" class="grid min-h-0 flex-1 content-start gap-3 overflow-auto md:grid-cols-2">
      <p v-if="!items.length" class="card muted p-10 text-center text-sm md:col-span-2">{{ $t('plugins.none') }}</p>
      <div v-for="p in items" :id="`plugin-${p.manifest.id}`" :key="p.manifest.id" class="card flex flex-col p-4" :class="highlight === p.manifest.id ? 'ring-2 ring-brand-500' : ''">
        <div class="flex items-start gap-2">
          <div class="min-w-0 flex-1">
            <p class="font-medium">{{ p.manifest.name }} <span class="muted text-xs">v{{ p.manifest.version }}</span></p>
            <p class="muted text-xs">{{ kind(p.manifest.type) }} · {{ p.manifest.author }} · {{ p.manifest.id }}</p>
          </div>
          <span class="badge" :class="{ 'badge-success': p.status === 'running' || p.status === 'ready', 'badge-danger': p.status === 'error' }">{{ statusText(p.status) }}</span>
        </div>
        <p class="mt-2 text-sm">{{ p.manifest.description }}</p>
        <p v-if="p.error" class="mt-2 break-all text-xs text-red-600">{{ p.error }}</p>
        <p class="muted mt-2 text-xs">
          <span v-if="p.signedBy" class="inline-flex items-center gap-1 text-emerald-600"><BadgeCheck class="h-3.5 w-3.5" />{{ $t('plugins.signed', { key: p.signedBy }) }}</span>
          <span v-else class="inline-flex items-center gap-1 text-amber-600"><ShieldAlert class="h-3.5 w-3.5" />{{ $t('plugins.unsigned') }}</span>
          · {{ $t('plugins.updated', { time: formatTime(p.updatedAt) }) }}
          <template v-if="p.manifest.permissions?.length"> · {{ $t('plugins.permissions', { list: p.manifest.permissions.join(', ') }) }}</template>
        </p>
        <div class="mt-3 flex gap-2">
          <button class="btn" :disabled="busy === p.manifest.id" @click="toggle(p)">{{ p.enabled ? $t('plugins.disable') : $t('plugins.enable') }}</button>
          <button v-if="p.manifest.config?.length" class="btn" @click="startConfig(p)">{{ $t('plugins.configure') }}</button>
          <button class="btn btn-danger ml-auto" @click="delTarget = p">{{ $t('plugins.remove') }}</button>
        </div>
      </div>
    </div>

    <div v-else class="card min-h-0 flex-1 overflow-auto">
      <p class="muted border-b border-[var(--border)] px-4 py-2 text-xs break-all">{{ $t('plugins.repo', { url: repoUrl || $t('plugins.repoUnset') }) }}</p>
      <p v-if="repoErr" class="p-6 text-sm text-red-600">{{ repoErr }}</p>
      <p v-else-if="repo === null" class="muted p-6 text-sm">{{ $t('common.loading') }}</p>
      <div v-for="r in repo ?? []" :id="`plugin-${r.id}`" :key="r.id" class="flex items-center gap-3 border-b border-[var(--border)] px-4 py-3 text-sm last:border-0" :class="highlight === r.id ? 'bg-brand-50 ring-2 ring-inset ring-brand-500' : ''">
        <div class="min-w-0 flex-1">
          <p class="font-medium">{{ r.name }} <span class="muted text-xs">{{ r.latest ? 'v' + r.latest : $t('plugins.incompatible') }}</span></p>
          <p class="muted text-xs">{{ r.description }}</p>
        </div>
        <span v-if="r.installed && !r.update" class="muted text-xs">{{ $t('plugins.installedVersion', { v: r.installed }) }}</span>
        <button v-else-if="r.latest" class="btn btn-primary" :disabled="busy === r.id" @click="installRepo(r)">{{ r.update ? $t('plugins.updateTo', { v: r.latest }) : $t('plugins.install') }}</button>
      </div>
    </div>

    <Modal v-model:open="cfgOpen" :title="$t('plugins.configTitle', { name: cfgTarget?.manifest.name ?? '' })" wide>
      <form v-if="cfgTarget" class="grid gap-3" @submit.prevent="saveConfig">
        <label v-for="f in cfgTarget.manifest.config" :key="f.key" class="field">
          <span>{{ f.label }}<span v-if="f.required" class="text-red-600"> *</span></span>
          <input v-if="f.type === 'bool'" v-model="cfgValues[f.key]" type="checkbox" class="ml-2" />
          <input v-else v-model="cfgValues[f.key]" class="input" :type="f.type === 'secret' ? 'password' : f.type === 'number' ? 'number' : 'text'" :placeholder="f.placeholder" autocomplete="off" />
          <span v-if="f.help" class="muted mt-0.5 block text-xs">{{ f.help }}</span>
        </label>
      </form>
      <template #footer>
        <button class="btn" @click="cfgOpen = false">{{ $t('common.cancel') }}</button>
        <button class="btn btn-primary" @click="saveConfig">{{ $t('common.save') }}</button>
      </template>
    </Modal>
    <Modal :open="!!delTarget" @update:open="(v) => { if (!v) delTarget = null }" :title="$t('plugins.removeTitle')" :description="$t('plugins.removeDesc', { name: delTarget?.manifest.name ?? '' })">
      <template #footer>
        <button class="btn" @click="delTarget = null">{{ $t('common.cancel') }}</button>
        <button class="btn btn-danger-solid" @click="remove">{{ $t('plugins.remove') }}</button>
      </template>
    </Modal>
  </section>
</template>
