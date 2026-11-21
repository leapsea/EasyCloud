<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed, ref, watch } from 'vue'
import Modal from '@/components/Modal.vue'
import { shareApi } from '@/api/share'
import type { FileEntry } from '@/api/files'
import { useToast } from '@/stores/toast'
const { t } = useI18n()

const props = defineProps<{ entry: FileEntry | null }>()
const emit = defineEmits<{ close: [] }>()
const toast = useToast()
const open = ref(false)
const form = ref({ password: '', expireDays: 7, maxDownloads: 0, usePassword: false })
const link = ref('')
const busy = ref(false)
watch(() => props.entry, (e) => { open.value = !!e; link.value = ''; form.value = { password: '', expireDays: 7, maxDownloads: 0, usePassword: false } })
watch(open, (o) => { if (!o) emit('close') })

const expiries = computed(() => [{ v: 1, l: t('shareDialog.days', { n: 1 }) }, { v: 7, l: t('shareDialog.days', { n: 7 }) }, { v: 30, l: t('shareDialog.days', { n: 30 }) }, { v: 0, l: t('shareDialog.forever') }])
function genPw() {
  const a = new Uint8Array(6); crypto.getRandomValues(a)
  form.value.password = Array.from(a, (b) => 'abcdefghjkmnpqrstuvwxyz23456789'[b % 31]).join('')
}
async function create() {
  busy.value = true
  try {
    const s = await shareApi.create({ path: props.entry!.path, password: form.value.usePassword ? form.value.password : '', expireDays: form.value.expireDays, maxDownloads: form.value.maxDownloads })
    link.value = location.origin + s.url
  } catch (e) { toast.error(e) } finally { busy.value = false }
}
const shareText = computed(() => link.value + (form.value.usePassword ? '\n' + t('shareDialog.codeLine', { code: form.value.password }) : ''))
async function copy() {
  try { await navigator.clipboard.writeText(shareText.value); toast.success(t('toast.copied'), t('toast.shareCopiedD')) } catch { toast.warn(t('toast.copyFailed'), t('toast.copyFailedD')) }
}
</script>

<template>
  <Modal v-model:open="open" :title="$t('shareDialog.title', { name: entry?.name ?? '' })" :description="$t('shareDialog.desc')">
    <form v-if="!link" class="grid gap-3 text-sm" @submit.prevent="create">
      <fieldset>
        <legend>{{ $t('shareDialog.expiry') }}</legend>
        <div class="mt-1 flex flex-wrap gap-2">
          <label v-for="x in expiries" :key="x.v" class="flex items-center gap-1 rounded border border-[var(--border)] px-2 py-1">
            <input v-model="form.expireDays" type="radio" :value="x.v" />{{ x.l }}
          </label>
        </div>
      </fieldset>
      <label class="flex items-center gap-2"><input v-model="form.usePassword" type="checkbox" @change="form.usePassword && !form.password && genPw()" />{{ $t('shareDialog.setCode') }}</label>
      <div v-if="form.usePassword" class="flex gap-2">
        <input v-model="form.password" class="input font-mono" maxlength="32" />
        <button type="button" class="btn" @click="genPw">{{ $t('shareDialog.random') }}</button>
      </div>
      <label class="field">{{ $t('shareDialog.maxDownloads') }}<input v-model.number="form.maxDownloads" type="number" min="0" class="input !w-32" /></label>
    </form>
    <div v-else class="grid gap-2">
      <p class="text-sm">{{ $t('shareDialog.created') }}</p>
      <textarea readonly class="input h-20 font-mono text-xs" :value="shareText" @focus="($event.target as HTMLTextAreaElement).select()" />
    </div>
    <template #footer>
      <button class="btn" @click="open = false">{{ link ? $t('common.done') : $t('common.cancel') }}</button>
      <button v-if="!link" class="btn btn-primary" :disabled="busy || (form.usePassword && !form.password)" @click="create">{{ $t('shareDialog.create') }}</button>
      <button v-else class="btn btn-primary" @click="copy">{{ $t('shares.copyLink') }}</button>
    </template>
  </Modal>
</template>
