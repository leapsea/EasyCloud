<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { Download, Maximize2, Minus, Music, X } from 'lucide-vue-next'
import AudioPlayer from '@/features/files/AudioPlayer.vue'
import { filesApi } from '@/api/files'
import { useAudio } from '@/stores/audio'

// One audio player for the whole app: a dialog when expanded, a small bar in
// the bottom-right corner when minimized. Both states render the same
// AudioPlayer (and so the same <audio> element), so minimizing, restoring and
// changing pages never interrupt playback. Not a reka Dialog: switching its
// modal state would remount the content and stop the audio.
const audio = useAudio()
const src = computed(() => (audio.entry ? filesApi.downloadUrl(audio.entry.path, true) : ''))

function download() {
  const e = audio.entry
  if (!e) return
  const a = document.createElement('a')
  a.href = filesApi.downloadUrl(e.path)
  a.download = e.name
  a.click()
}
function onKey(ev: KeyboardEvent) {
  if (ev.key === 'Escape' && audio.entry && !audio.minimized) { ev.preventDefault(); audio.minimized = true }
}
onMounted(() => window.addEventListener('keydown', onKey))
// Unmounted when leaving the signed-in shell (logout): stop for good.
onBeforeUnmount(() => { window.removeEventListener('keydown', onKey); audio.close() })
</script>

<template>
  <Teleport to="body">
    <template v-if="audio.entry">
      <div v-if="!audio.minimized" class="fixed inset-0 z-40 bg-slate-900/40" @click="audio.minimized = true" />
      <div
        role="dialog" :aria-label="audio.entry.name" :aria-modal="!audio.minimized"
        class="fixed z-50 border border-[var(--border)] bg-[var(--surface)] shadow-[var(--shadow-pop)]"
        :class="audio.minimized
          ? 'bottom-4 right-4 w-[min(22rem,calc(100vw-2rem))] rounded-xl p-2.5'
          : 'left-1/2 top-1/2 w-[min(42rem,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 rounded-2xl p-5 sm:p-6'"
      >
        <div v-if="!audio.minimized" class="mb-4 flex items-start gap-3">
          <h2 class="min-w-0 flex-1 break-all text-base font-semibold leading-6">{{ audio.entry.name }}</h2>
          <button class="btn btn-sm !px-1.5" :title="$t('player.minimize')" :aria-label="$t('player.minimize')" @click="audio.minimized = true"><Minus class="h-4 w-4" /></button>
          <button class="btn btn-sm !px-1.5" :title="$t('common.close')" :aria-label="$t('common.close')" @click="audio.close()"><X class="h-4 w-4" /></button>
        </div>

        <div class="flex items-center gap-1">
          <div class="min-w-0 flex-1">
            <AudioPlayer :key="audio.entry.path" :src="src" :name="audio.entry.name" :mini="audio.minimized">
              <template #title>
                <p class="flex items-center gap-1 truncate text-xs font-medium" :title="audio.entry.name"><Music class="h-3 w-3 shrink-0 text-brand-600" />{{ audio.entry.name }}</p>
              </template>
            </AudioPlayer>
          </div>
          <template v-if="audio.minimized">
            <button class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--surface-muted)] hover:text-[var(--text)]" :title="$t('player.restore')" :aria-label="$t('player.restore')" @click="audio.minimized = false"><Maximize2 class="h-3.5 w-3.5" /></button>
            <button class="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--surface-muted)] hover:text-[var(--text)]" :title="$t('common.close')" :aria-label="$t('common.close')" @click="audio.close()"><X class="h-3.5 w-3.5" /></button>
          </template>
        </div>

        <div v-if="!audio.minimized" class="mt-5 flex flex-wrap items-center justify-end gap-2">
          <p class="muted mr-auto text-xs">{{ $t('player.backgroundHint') }}</p>
          <button class="btn" @click="download"><Download class="h-4 w-4" />{{ $t('common.download') }}</button>
          <button class="btn btn-primary" @click="audio.minimized = true"><Minus class="h-4 w-4" />{{ $t('player.minimize') }}</button>
        </div>
      </div>
    </template>
  </Teleport>
</template>
