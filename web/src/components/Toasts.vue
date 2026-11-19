<script setup lang="ts">
import { actionLabel } from '@/api/client'
import { CircleAlert, CircleCheck, Info, X } from 'lucide-vue-next'
import { useRouter } from 'vue-router'
import { useToast, type Toast } from '@/stores/toast'

const toast = useToast()
const router = useRouter()

function go(t: Toast) {
  if (!t.action) return
  router.push({ path: t.action.route, query: t.action.focus ? { focus: t.action.focus } : {} })
  toast.dismiss(t.id)
}
function copyId(t: Toast) { if (t.requestId) navigator.clipboard?.writeText(t.requestId) }
</script>

<template>
  <div class="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-[min(92vw,380px)] flex-col gap-2">
    <TransitionGroup name="list">
      <div
        v-for="t in toast.items" :key="t.id"
        class="card pointer-events-auto flex items-start gap-3 p-3 text-sm shadow-lg"
        :class="{ 'border-red-300': t.kind === 'error', 'border-emerald-300': t.kind === 'success' }"
        role="status"
      >
        <CircleAlert v-if="t.kind === 'error'" class="mt-0.5 h-4 w-4 shrink-0 text-red-500" /><CircleCheck v-else-if="t.kind === 'success'" class="mt-0.5 h-4 w-4 shrink-0 text-emerald-500" /><Info v-else class="mt-0.5 h-4 w-4 shrink-0 text-brand-500" />
        <div class="min-w-0 flex-1">
          <p>{{ t.text }}</p>
          <div class="mt-2 flex gap-2" v-if="t.action || t.requestId">
            <button v-if="t.action" class="btn btn-primary btn-sm" @click="go(t)">{{ actionLabel(t.action) }}</button>
            <button v-if="t.requestId" class="btn btn-sm" :title="$t('toast.copyIdHint')" @click="copyId(t)">{{ $t('toast.copyId') }}</button>
          </div>
        </div>
        <button class="muted" :aria-label="$t('common.close')" @click="toast.dismiss(t.id)"><X class="h-3.5 w-3.5" /></button>
      </div>
    </TransitionGroup>
  </div>
</template>
