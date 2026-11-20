<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { ArrowDownUp } from 'lucide-vue-next'
import { transferOpen, useTransferCounts } from './transferPanel'

// Sidebar entry to the transfer panel: a count while work is running, a red dot
// after failures.
const { t } = useI18n()
const { busy, failed } = useTransferCounts()
const label = computed(() => (busy.value ? t('transfer.running', { n: busy.value }) : failed.value ? t('transfer.failedN', { n: failed.value }) : t('transfer.title')))
</script>

<template>
  <button
    class="btn btn-sm relative !px-1.5" :class="transferOpen && 'bg-[var(--surface-sunken)]'"
    :title="label" :aria-label="label" :aria-pressed="transferOpen" @click="transferOpen = !transferOpen"
  >
    <ArrowDownUp class="h-3.5 w-3.5" />
    <span
      v-if="busy" class="absolute -right-1.5 -top-1.5 flex h-4 min-w-4 items-center justify-center rounded-full bg-brand-600 px-1 text-[10px] font-medium leading-none tabular-nums text-[var(--on-brand)]"
    >{{ busy > 99 ? '99+' : busy }}</span>
    <span v-else-if="failed" class="absolute -right-0.5 -top-0.5 h-2 w-2 rounded-full bg-red-500 ring-2 ring-[var(--surface)]" />
  </button>
</template>
