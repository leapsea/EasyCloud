<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { filesApi } from '@/api/files'
import { useAuth } from '@/stores/auth'
import { formatSize } from './format'

const auth = useAuth()
const used = ref<number | null>(null)
let t: number | undefined
async function load() { if (auth.user) used.value = (await filesApi.usage(auth.user.home).catch(() => null))?.bytes ?? null }
onMounted(() => { t = window.setInterval(load, 60_000) })
watch(() => auth.user?.id, () => { used.value = null; load() }, { immediate: true })
onBeforeUnmount(() => clearInterval(t))
</script>

<template>
  <RouterLink v-if="auth.user && used !== null" to="/account" class="block rounded-lg px-3 py-2 text-xs hover:bg-[var(--surface-muted)]">
    <p class="muted">{{ $t('quota.personal') }} {{ formatSize(used) }}{{ auth.user.quotaBytes ? ` / ${formatSize(auth.user.quotaBytes)}` : '' }}</p>
    <div v-if="auth.user.quotaBytes" class="mt-1 h-1 overflow-hidden rounded bg-[var(--border)]">
      <div class="h-full" :class="used > auth.user.quotaBytes * 0.9 ? 'bg-red-500' : 'bg-brand-500'" :style="{ width: Math.min(100, (used / auth.user.quotaBytes) * 100) + '%' }" />
    </div>
  </RouterLink>
</template>
