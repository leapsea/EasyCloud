<script setup lang="ts">
import { copyText as clipCopy } from '@/components/clipboard'
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import { useLocalePref } from '@/i18n'
import { Check, Monitor, Moon, Sun } from 'lucide-vue-next'
import { ACCENTS, useTheme } from '@/theme/theme'
import { GALLERY_COL_CHOICES, galleryCols } from '@/features/files/galleryPref'
import { KeyRound, ShieldCheck } from 'lucide-vue-next'
import QRCode from 'qrcode'
import { computed, onMounted, ref } from 'vue'
import { authApi, filesApi } from '@/api/files'
import { errorMessage } from '@/api/client'
import { appPasswordApi, type AppPassword } from '@/api/share'
import Modal from '@/components/Modal.vue'
import PasswordInput from '@/components/PasswordInput.vue'
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
  try { await appPasswordApi.remove(a.id); toast.success(t('toast.appPwRevoked'), t('toast.appPwRevokedD')); loadAppPasswords() } catch (e) { toast.error(e) }
}
async function copyText(text: string) { try { await clipCopy(text); toast.success(t('toast.copied'), t('toast.copiedD')) } catch { toast.warn(t('toast.copyFailed'), t('toast.copyFailedD')) } }

onMounted(async () => {
  if (auth.user) usage.value = await filesApi.usage(auth.user.home).catch(() => null)
  loadAppPasswords()
})
// Two-step verification (authenticator app). Binding: password, then scan the
// QR code and confirm with a code. Once bound, the account signs in with the
// password or a current code. Unbinding: password or a code.
const tf = ref({ open: false, step: 'password' as 'password' | 'scan' | 'disable', password: '', code: '', secret: '', qr: '', busy: false, error: '' })
function startBind() { tf.value = { ...tf.value, open: true, step: 'password', password: '', code: '', secret: '', qr: '', error: '' } }
function startUnbind() { tf.value = { ...tf.value, open: true, step: 'disable', password: '', code: '', error: '' } }
async function tfNext() {
  const s = tf.value
  s.busy = true; s.error = ''
  try {
    if (s.step === 'password') {
      const r = await authApi.totpSetup(s.password)
      s.secret = r.secret
      s.qr = await QRCode.toDataURL(r.uri, { margin: 1, width: 200 })
      s.step = 'scan'; s.password = ''
    } else if (s.step === 'scan') {
      await authApi.totpEnable(s.code.replace(/\s/g, ''))
      s.open = false
      toast.success(t('toast.tfOn'), t('toast.tfOnD'))
      await auth.check()
    } else {
      await authApi.totpDisable(s.password ? { password: s.password } : { code: s.code.replace(/\s/g, '') })
      s.open = false
      toast.success(t('toast.tfOff'), t('toast.tfOffD'))
      await auth.check()
    }
  } catch (e) { s.error = errorMessage(e) } finally { s.busy = false }
}
const secretGroups = computed(() => tf.value.secret.replace(/(.{4})/g, '$1 ').trim())

async function change() {
  if (pw.value.next !== pw.value.confirm) { toast.warn(t('toast.pwMismatch'), t('toast.pwMismatchD')); return }
  busy.value = true
  try {
    await authApi.changePassword(pw.value.current, pw.value.next)
    toast.success(t('toast.pwChanged'), t('toast.pwChangedD'))
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
    <div class="card grid gap-3 p-4 text-sm">
      <div class="flex items-center gap-2">
        <h2 class="font-medium">{{ $t('account.tfTitle') }}</h2>
        <span v-if="auth.user?.twoFactor" class="badge badge-success"><ShieldCheck class="h-3 w-3" />{{ $t('account.tfOn') }}</span>
        <span v-else class="badge">{{ $t('account.tfOff') }}</span>
      </div>
      <p class="muted text-xs leading-5">{{ auth.user?.twoFactor ? $t('account.tfOnHelp') : $t('account.tfHelp') }}</p>
      <div>
        <button v-if="auth.user?.twoFactor" class="btn btn-danger" @click="startUnbind">{{ $t('account.tfUnbind') }}</button>
        <button v-else class="btn btn-primary" @click="startBind">{{ $t('account.tfBind') }}</button>
      </div>
    </div>
    <Modal v-model:open="tf.open" :title="tf.step === 'disable' ? $t('account.tfUnbind') : $t('account.tfBind')">
      <form class="grid gap-3 text-sm" @submit.prevent="tfNext">
        <template v-if="tf.step === 'password'">
          <p class="muted text-xs">{{ $t('account.tfStep1') }}</p>
          <PasswordInput v-model="tf.password" :placeholder="$t('account.currentPw')" autocomplete="current-password" autofocus />
        </template>
        <template v-else-if="tf.step === 'scan'">
          <p class="muted text-xs leading-5">{{ $t('account.tfStep2') }}</p>
          <img :src="tf.qr" :alt="$t('account.tfQrAlt')" class="mx-auto h-48 w-48 rounded-lg border border-[var(--border)] bg-white p-1" />
          <div class="text-center">
            <p class="muted text-xs">{{ $t('account.tfManual') }}</p>
            <code class="mt-1 inline-block select-all break-all rounded bg-[var(--surface-muted)] px-2 py-1 font-mono text-xs">{{ secretGroups }}</code>
            <button type="button" class="ml-2 text-xs text-brand-600 hover:underline" @click="copyText(tf.secret)">{{ $t('common.copy') }}</button>
          </div>
          <input v-model="tf.code" class="input text-center font-mono tracking-[0.3em]" :placeholder="$t('login.codePlaceholder')" inputmode="numeric" autocomplete="one-time-code" maxlength="7" autofocus />
        </template>
        <template v-else>
          <p class="muted text-xs">{{ $t('account.tfUnbindHelp') }}</p>
          <PasswordInput v-model="tf.password" :placeholder="$t('account.currentPw')" autocomplete="current-password" />
          <input v-model="tf.code" class="input font-mono tracking-[0.3em]" :placeholder="$t('account.tfOrCode')" inputmode="numeric" autocomplete="one-time-code" maxlength="7" />
        </template>
        <p v-if="tf.error" class="text-xs text-red-600" role="alert">{{ tf.error }}</p>
      </form>
      <template #footer>
        <button class="btn" @click="tf.open = false">{{ $t('common.cancel') }}</button>
        <button
          class="btn" :class="tf.step === 'disable' ? 'btn-danger-solid' : 'btn-primary'"
          :disabled="tf.busy || (tf.step === 'password' && !tf.password) || (tf.step === 'scan' && tf.code.replace(/\s/g, '').length !== 6) || (tf.step === 'disable' && !tf.password && tf.code.replace(/\s/g, '').length !== 6)"
          @click="tfNext"
        >{{ tf.step === 'password' ? $t('account.tfNext') : tf.step === 'scan' ? $t('account.tfConfirm') : $t('account.tfUnbind') }}</button>
      </template>
    </Modal>

    <form class="card grid gap-3 p-4" @submit.prevent="change">
      <h2 class="font-medium">{{ $t('account.changePw') }}</h2>
      <PasswordInput v-model="pw.current" :placeholder="$t('account.currentPw')" autocomplete="current-password" />
      <PasswordInput v-model="pw.next" :placeholder="$t('account.newPw')" autocomplete="new-password" />
      <PasswordInput v-model="pw.confirm" :placeholder="$t('account.confirmPw')" autocomplete="new-password" />
      <button class="btn btn-primary justify-self-start" :disabled="busy || !pw.current || pw.next.length < 8">{{ $t('account.changePw') }}</button>
    </form>
  </section>
</template>
