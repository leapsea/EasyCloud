<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import { useLocalePref } from '@/i18n'
import { Check, Monitor, Moon, Sun } from 'lucide-vue-next'
import { ACCENTS, useTheme } from '@/theme/theme'
import { GALLERY_COL_CHOICES, galleryCols } from '@/features/files/galleryPref'
import { KeyRound } from 'lucide-vue-next'
import { computed, onMounted, ref } from 'vue'
import { authApi, filesApi } from '@/api/files'
import { appPasswordApi, type AppPassword } from '@/api/share'
import Modal from '@/components/Modal.vue'
import { formatTime } from '@/components/format'
import { useAuth } from '@/stores/auth'
import { useToast } from '@/stores/toast'
import { formatSize } from '@/components/format'
const { t } = useI18n()

const auth = useAuth()
const toast = useToast()
const usage = ref<{ bytes: number; files: number } | null>(null)
const pw = ref({ current: '', next: '', confirm: '' })
const busy = ref(false)

const theme = useTheme()
const modes = [
  { value: 'system' as const, label: 'appearance.mode.system', icon: Monitor },
  { value: 'light' as const, label: 'appearance.mode.light', icon: Sun },
  { value: 'dark' as const, label: 'appearance.mode.dark', icon: Moon },
]
const lang = useLocalePref()
const langs = [
  { value: 'auto' as const, label: 'appearance.lang.auto' },
  { value: 'zh-CN' as const, label: 'appearance.lang.zh' },
  { value: 'en' as const, label: 'appearance.lang.en' },
]
const isCustom = computed(() => !ACCENTS.some((a) => a.value === theme.state.accent))
const appPasswords = ref<AppPassword[]>([])
const newName = ref('')
const created = ref<{ name: string; password: string } | null>(null)
const smbHost = location.hostname
async function loadAppPasswords() { appPasswords.value = (await appPasswordApi.list().catch(() => ({ items: [] }))).items }
async function createAppPassword() {
  try {
    const r = await appPasswordApi.create(newName.value.trim())
    created.value = { name: r.item.name, password: r.password }
    newName.value = ''
    loadAppPasswords()
  } catch (e) { toast.error(e) }
}
async function removeAppPassword(a: AppPassword) {
  try { await appPasswordApi.remove(a.id); toast.success(t('account.appPwRevoked')); loadAppPasswords() } catch (e) { toast.error(e) }
}
async function copyText(text: string) { try { await navigator.clipboard.writeText(text); toast.success(t('common.copied')) } catch { /* ignore */ } }

onMounted(async () => {
  if (auth.user) usage.value = await filesApi.usage(auth.user.home).catch(() => null)
  loadAppPasswords()
})
async function change() {
  if (pw.value.next !== pw.value.confirm) { toast.push({ kind: 'error', text: t('account.pwMismatch') }); return }
  busy.value = true
  try {
    await authApi.changePassword(pw.value.current, pw.value.next)
    toast.success(t('account.pwChanged'))
    pw.value = { current: '', next: '', confirm: '' }
  } catch (e) { toast.error(e) } finally { busy.value = false }
}
</script>

<template>
  <section class="flex max-w-xl flex-col gap-4">
    <PageHeader :title="$t('nav.account')" class="!-mb-2" />
    <div v-if="auth.user" class="card p-4 text-sm">
      <dl class="grid grid-cols-[minmax(6rem,max-content)_1fr] gap-x-4 gap-y-2">
        <dt class="muted">{{ $t('login.username') }}</dt><dd>{{ auth.user.username }}</dd>
        <dt class="muted">{{ $t('users.displayName') }}</dt><dd>{{ auth.user.displayName }}</dd>
        <dt class="muted">{{ $t('users.col.role') }}</dt><dd>{{ auth.user.isAdmin ? $t('users.role.admin') : $t('users.role.user') }}</dd>
        <dt class="muted">{{ $t('quota.personal') }}</dt>
        <dd>
          <RouterLink class="font-mono text-xs hover:underline" :to="{ path: '/files', query: { path: auth.user.home } }">{{ auth.user.home }}</RouterLink>
          <p v-if="usage" class="muted text-xs">{{ $t('account.used', { size: formatSize(usage.bytes), n: usage.files }) }}{{ auth.user.quotaBytes ? ' / ' + $t('account.quota', { size: formatSize(auth.user.quotaBytes) }) : '' }}</p>
        </dd>
      </dl>
    </div>
    <div class="card grid gap-4 p-4 text-sm">
      <div>
        <h2 class="font-medium">{{ $t('appearance.title') }}</h2>
        <p class="muted mt-0.5 text-xs">{{ $t('appearance.scope') }}</p>
      </div>
      <div class="field">
        {{ $t('appearance.language') }}
        <div class="inline-flex w-fit rounded-lg border border-[var(--border)] bg-[var(--surface-sunken)] p-0.5" role="radiogroup" :aria-label="$t('appearance.language')">
          <button
            v-for="l in langs" :key="l.value" role="radio" :aria-checked="lang.pref.value === l.value"
            class="flex h-7 items-center gap-1.5 rounded-md px-3 text-[13px] font-normal transition-colors"
            :class="lang.pref.value === l.value ? 'bg-[var(--surface)] text-[var(--text)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text)]'"
            @click="lang.setPref(l.value)"
          >{{ $t(l.label) }}</button>
        </div>
      </div>
      <div class="field">
        {{ $t('appearance.modeLabel') }}
        <div class="inline-flex w-fit rounded-lg border border-[var(--border)] bg-[var(--surface-sunken)] p-0.5" role="radiogroup" :aria-label="$t('appearance.modeLabel')">
          <button
            v-for="m in modes" :key="m.value" role="radio" :aria-checked="theme.state.mode === m.value"
            class="flex h-7 items-center gap-1.5 rounded-md px-3 text-[13px] font-normal transition-colors"
            :class="theme.state.mode === m.value ? 'bg-[var(--surface)] text-[var(--text)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text)]'"
            @click="theme.setMode(m.value)"
          ><component :is="m.icon" class="h-3.5 w-3.5" />{{ $t(m.label) }}</button>
        </div>
      </div>
      <div class="field">
        {{ $t('appearance.accent') }}
        <div class="flex flex-wrap items-center gap-2.5" role="radiogroup" :aria-label="$t('appearance.accent')">
          <button
            v-for="a in ACCENTS" :key="a.value" role="radio" :aria-checked="theme.state.accent === a.value" :title="$t(`theme.accent.${a.key}`)" :aria-label="$t(`theme.accent.${a.key}`)"
            class="flex h-7 w-7 items-center justify-center rounded-full ring-offset-2 ring-offset-[var(--surface)] transition-transform hover:scale-110"
            :class="theme.state.accent === a.value && 'ring-2'" :style="{ background: a.value, '--tw-ring-color': a.value }"
            @click="theme.setAccent(a.value)"
          ><Check v-if="theme.state.accent === a.value" class="h-3.5 w-3.5 text-white" :stroke-width="3" /></button>
          <label
            class="relative flex h-7 cursor-pointer items-center gap-1.5 rounded-full border border-dashed border-[var(--border)] px-2.5 text-xs font-normal text-[var(--text-muted)] hover:text-[var(--text)]"
            :class="isCustom && 'border-solid ring-2 ring-offset-2 ring-offset-[var(--surface)]'" :style="isCustom ? { '--tw-ring-color': theme.state.accent } : {}" :title="$t('appearance.customTitle')"
          >
            <span class="h-3.5 w-3.5 rounded-full" :style="{ background: isCustom ? theme.state.accent : 'conic-gradient(red, yellow, lime, aqua, blue, magenta, red)' }" />
            {{ $t('appearance.custom') }}
            <input type="color" class="absolute inset-0 cursor-pointer opacity-0" :value="theme.state.accent" @input="theme.setAccent(($event.target as HTMLInputElement).value)" />
          </label>
        </div>
      </div>
      <div class="field">
        {{ $t('appearance.galleryCols') }}
        <div class="inline-flex w-fit rounded-lg border border-[var(--border)] bg-[var(--surface-sunken)] p-0.5" role="radiogroup" :aria-label="$t('appearance.galleryCols')">
          <button
            v-for="n in GALLERY_COL_CHOICES" :key="n" role="radio" :aria-checked="galleryCols === n"
            class="flex h-7 min-w-8 items-center justify-center rounded-md px-2.5 text-[13px] font-normal tabular-nums transition-colors"
            :class="galleryCols === n ? 'bg-[var(--surface)] text-[var(--text)] shadow-sm' : 'text-[var(--text-muted)] hover:text-[var(--text)]'"
            @click="galleryCols = n"
          >{{ n ? n : $t('appearance.galleryAuto') }}</button>
        </div>
        <span class="hint">{{ $t('appearance.galleryHint') }}</span>
      </div>
      <div class="flex flex-wrap items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--surface-muted)] p-3">
        <span class="muted text-xs">{{ $t('appearance.preview') }}</span>
        <button class="btn btn-primary btn-sm">{{ $t('appearance.primary') }}</button>
        <button class="btn btn-sm">{{ $t('appearance.secondary') }}</button>
        <span class="badge badge-brand">{{ $t('appearance.badge') }}</span>
        <a class="text-xs text-brand-600 hover:underline" href="javascript:void 0">{{ $t('appearance.link') }}</a>
        <input type="checkbox" checked :aria-label="$t('appearance.checkbox')" />
      </div>
    </div>

    <div class="card grid gap-3 p-4 text-sm">
      <h2 class="font-medium">{{ $t('account.netDrive') }}</h2>
      <p class="muted text-xs">{{ $t('account.netDriveHelp') }}</p>
      <dl class="grid grid-cols-[minmax(5rem,max-content)_1fr] items-center gap-x-4 gap-y-2">
        <dt class="muted">SMB</dt>
        <dd class="flex items-center gap-2"><code class="truncate rounded bg-[var(--surface-muted)] px-2 py-1 text-xs">smb://{{ smbHost }}/{{ auth.user?.username }}</code><span class="muted text-xs">{{ $t('account.smbNeedsPlugin') }}</span></dd>
        <dt class="muted">{{ $t('login.username') }}</dt><dd><code class="text-xs">{{ auth.user?.username }}</code></dd>
      </dl>
      <form class="flex gap-2" @submit.prevent="createAppPassword">
        <input v-model="newName" class="input" :placeholder="$t('account.deviceName')" maxlength="64" />
        <button class="btn btn-primary shrink-0" :disabled="!newName.trim()">{{ $t('account.genAppPw') }}</button>
      </form>
      <ul v-if="appPasswords.length" class="divide-y divide-[var(--border)] rounded-lg border border-[var(--border)]">
        <li v-for="a in appPasswords" :key="a.id" class="flex items-center gap-2 px-3 py-2">
          <span class="flex flex-1 items-center gap-2"><KeyRound class="h-4 w-4 text-[var(--text-muted)]" :stroke-width="1.75" />{{ a.name }}</span>
          <span class="muted text-xs">{{ a.lastUsedAt ? $t('account.lastUsed', { time: formatTime(a.lastUsedAt) }) : $t('account.unused') }}</span>
          <button class="btn btn-danger btn-sm" @click="removeAppPassword(a)">{{ $t('grants.revoke') }}</button>
        </li>
      </ul>
    </div>
    <Modal :open="!!created" @update:open="(v) => { if (!v) created = null }" :title="$t('account.appPwTitle', { name: created?.name ?? '' })" :description="$t('account.appPwOnce')">
      <div class="flex gap-2"><code class="input flex-1 select-all font-mono">{{ created?.password }}</code><button class="btn" @click="created && copyText(created.password)">{{ $t('common.copy') }}</button></div>
      <template #footer><button class="btn btn-primary" @click="created = null">{{ $t('account.saved') }}</button></template>
    </Modal>
    <form class="card grid gap-3 p-4" @submit.prevent="change">
      <h2 class="font-medium">{{ $t('account.changePw') }}</h2>
      <input v-model="pw.current" type="password" class="input" :placeholder="$t('account.currentPw')" autocomplete="current-password" />
      <input v-model="pw.next" type="password" class="input" :placeholder="$t('account.newPw')" autocomplete="new-password" />
      <input v-model="pw.confirm" type="password" class="input" :placeholder="$t('account.confirmPw')" autocomplete="new-password" />
      <button class="btn btn-primary justify-self-start" :disabled="busy || !pw.current || pw.next.length < 8">{{ $t('account.changePw') }}</button>
    </form>
  </section>
</template>
