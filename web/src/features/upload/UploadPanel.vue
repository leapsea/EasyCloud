<script setup lang="ts">
import { actionLabel } from '@/api/client'
import { useI18n } from 'vue-i18n'
import { Pause, Play, X } from 'lucide-vue-next'
import { computed, onBeforeUnmount, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ApiError, errorMessage } from '@/api/client'
import { useUploads, type UploadItem } from '@/stores/upload'
import { useAuth } from '@/stores/auth'
import { formatSize } from '@/components/format'
import { useTasks, type Task } from '@/stores/tasks'
import { transferOpen, useTransferCounts } from './transferPanel'
const { t } = useI18n()
const i18n = t

const up = useUploads()
const auth = useAuth()
const router = useRouter()
const tasks = useTasks()
const { busy, failed: failedAll, total } = useTransferCounts()

// Opens when new work starts; once everything has finished it hides after a
// short pause, unless something failed in this round (then it stays until
// closed). Hovering the panel holds it open. Hiding never clears the list.
const HIDE_AFTER = 4000
let hideTimer: number | undefined
let failBase = 0 // failures already present when the current round started
let hovering = false
function scheduleHide() {
  clearTimeout(hideTimer)
  if (busy.value || failedAll.value > failBase || hovering) return
  hideTimer = window.setTimeout(() => { transferOpen.value = false }, HIDE_AFTER)
}
watch(busy, (n, old) => {
  if (n > old) {
    if (!old) failBase = failedAll.value
    transferOpen.value = true
    clearTimeout(hideTimer)
  } else if (n === 0 && old > 0) scheduleHide()
})
function onEnter() { hovering = true; clearTimeout(hideTimer) }
function onLeave() { hovering = false; if (!busy.value && transferOpen.value) scheduleHide() }
onBeforeUnmount(() => clearTimeout(hideTimer))

// One bar for uploads and tasks still in progress.
const progress = computed(() => {
  let done = 0, all = 0
  for (const i of up.items) if (i.state === 'uploading' || i.state === 'queued' || i.state === 'paused') { done += i.sent; all += i.size }
  for (const t of tasks.visible) if ((t.status === 'running' || t.status === 'queued') && t.totalBytes) { done += Math.min(t.doneBytes, t.totalBytes); all += t.totalBytes }
  return all ? done / all : 0
})
const kindLabel = (k: Task['kind']) => t(`tasks.kind.${k}`)
const taskPct = (t: Task) => (t.totalBytes ? Math.min(100, Math.round((t.doneBytes / t.totalBytes) * 100)) : t.status === 'done' ? 100 : 0)
const taskName = (t: Task) => t.params.sources.length > 1 ? i18n('transfer.andOthers', { name: t.params.sources[0].split('/').pop() ?? '', n: t.params.sources.length }) : t.params.sources[0].split('/').pop()
function clearAll() { up.clearFinished(); tasks.clearFinished().catch(() => {}) }
const status = computed(() => (busy.value ? t('transfer.running', { n: busy.value }) : failedAll.value ? t('transfer.failedN', { n: failedAll.value }) : total.value ? t('transfer.allDone') : ''))

function pct(i: UploadItem) { return i.size ? Math.round((i.sent / i.size) * 100) : 100 }
function action(i: UploadItem) {
  const a = i.error instanceof ApiError ? i.error.action : undefined
  if (!a || (a.role === 'admin' && !auth.user?.isAdmin)) return undefined
  return a
}
function go(i: UploadItem) {
  const a = action(i)
  if (a) router.push({ path: a.route, query: a.focus ? { focus: a.focus } : {} })
}
</script>

<template>
  <Transition name="fade">
    <div
      v-if="transferOpen" class="card fixed bottom-4 left-4 z-50 w-[min(92vw,380px)] shadow-xl sm:left-auto sm:right-4 sm:bottom-24"
      role="region" :aria-label="$t('transfer.title')" @mouseenter="onEnter" @mouseleave="onLeave"
    >
      <header class="flex items-center gap-2 border-b border-[var(--border)] px-3 py-2 text-sm">
        <span class="font-medium">{{ $t('transfer.title') }}<template v-if="status"> · {{ status }}</template></span>
        <div v-if="busy" class="h-1 flex-1 overflow-hidden rounded bg-[var(--border)]">
          <div class="h-full bg-brand-600 transition-[width] duration-300" :style="{ width: `${progress * 100}%` }" />
        </div>
        <span v-else class="flex-1" />
        <button v-if="total" class="muted text-xs hover:text-[var(--text)]" @click="clearAll">{{ $t('transfer.clear') }}</button>
        <button class="muted rounded p-0.5 hover:text-[var(--text)]" :title="$t('common.close')" :aria-label="$t('common.close')" @click="transferOpen = false"><X class="h-4 w-4" /></button>
      </header>
      <p v-if="!total" class="muted px-4 py-6 text-center text-sm">{{ $t('transfer.empty') }}</p>
      <ul v-else class="max-h-72 overflow-auto p-2 text-sm">
        <li v-for="t in tasks.visible" :key="t.id" class="rounded-lg px-2 py-2 hover:bg-[var(--surface-muted)]">
          <div class="flex items-center gap-2">
            <span class="rounded bg-[var(--border)] px-1 text-xs">{{ kindLabel(t.kind) }}</span>
            <span class="min-w-0 flex-1 truncate" :title="t.params.sources.join('\n')">{{ taskName(t) }}</span>
            <span class="muted text-xs tabular-nums">
              <template v-if="t.status === 'running'">{{ t.totalBytes ? taskPct(t) + '%' : formatSize(t.doneBytes) }}</template>
              <template v-else-if="t.status === 'queued'">{{ $t('transfer.queued') }}</template>
              <template v-else-if="t.status === 'done'">{{ $t('transfer.done') }}</template>
              <template v-else-if="t.status === 'cancelled'">{{ $t('transfer.cancelled') }}</template>
            </span>
            <button v-if="t.status === 'running' || t.status === 'queued'" class="muted" :title="$t('common.cancel')" :aria-label="$t('common.cancel')" @click="tasks.cancel(t.id)"><X class="h-3.5 w-3.5" /></button>
          </div>
          <div v-if="t.status === 'running'" class="mt-1 h-1 overflow-hidden rounded bg-[var(--border)]">
            <div class="h-full bg-emerald-500 transition-[width] duration-300" :style="{ width: `${taskPct(t)}%` }" />
          </div>
          <p v-if="t.status === 'failed'" class="mt-1 text-xs text-red-600">{{ $t('transfer.failedMsg', { message: t.message ?? '' }) }}</p>
        </li>
        <TransitionGroup name="list">
          <li v-for="i in up.items" :key="i.id" class="rounded-lg px-2 py-2 hover:bg-[var(--surface-muted)]">
            <div class="flex items-center gap-2">
              <span class="min-w-0 flex-1 truncate" :title="i.finalPath ?? i.name">{{ i.name }}</span>
              <span class="muted text-xs tabular-nums">
                <template v-if="i.state === 'uploading'">{{ formatSize(i.speed) }}/s · {{ pct(i) }}%</template>
                <template v-else-if="i.state === 'done'">{{ $t('transfer.done') }}</template>
                <template v-else-if="i.state === 'paused'">{{ $t('transfer.paused', { pct: pct(i) }) }}</template>
                <template v-else-if="i.state === 'queued'">{{ $t('transfer.queued') }}</template>
              </span>
              <button v-if="i.state === 'uploading'" class="muted" :title="$t('transfer.pause')" :aria-label="$t('transfer.pause')" @click="up.pause(i.id)"><Pause class="h-3.5 w-3.5" /></button>
              <button v-if="i.state === 'paused' || i.state === 'error'" class="muted" :title="$t('transfer.resume')" :aria-label="$t('transfer.resume')" @click="up.resume(i.id)"><Play class="h-3.5 w-3.5" /></button>
              <button v-if="i.state !== 'done'" class="muted" :title="$t('common.cancel')" :aria-label="$t('common.cancel')" @click="up.cancel(i.id)"><X class="h-3.5 w-3.5" /></button>
            </div>
            <div v-if="i.state !== 'done' && i.state !== 'error'" class="mt-1 h-1 overflow-hidden rounded bg-[var(--border)]">
              <div class="h-full bg-brand-600 transition-[width] duration-300" :style="{ width: `${pct(i)}%` }" />
            </div>
            <div v-if="i.state === 'error'" class="mt-1 flex items-center gap-2 text-xs text-red-600">
              <span class="flex-1">{{ errorMessage(i.error) }}{{ i.error instanceof ApiError && i.error.action?.role === 'admin' && !auth.user?.isAdmin ? $t('common.contactAdmin') : '' }}</span>
              <button v-if="action(i)" class="btn btn-primary btn-sm" @click="go(i)">{{ actionLabel(action(i)!) }}</button>
            </div>
          </li>
        </TransitionGroup>
      </ul>
    </div>
  </Transition>
</template>
