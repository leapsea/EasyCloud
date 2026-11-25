<script setup lang="ts">
import { copyText as clipCopy } from '@/components/clipboard'
import { computed, onBeforeUnmount, reactive, ref } from 'vue'
import { actionLabel } from '@/api/client'
import { CircleCheck, CircleX, Info, TriangleAlert, X } from 'lucide-vue-next'
import { useRouter } from 'vue-router'
import { useToast, type Toast } from '@/stores/toast'

// Toasts in the top-right corner, stacked: the newest in front, up to two
// older ones peeking out below it. Hovering (or focusing) the stack fans it
// out into a list and pauses the timers. Colours follow the kind: success,
// info, warn, error.
const toast = useToast()
const router = useRouter()
const icons = { success: CircleCheck, info: Info, warn: TriangleAlert, error: CircleX }

const PEEK = 10 // px of each older card visible below the front one
const GAP = 10
const VISIBLE = 3

const expanded = ref(false)
// Natural card heights, measured on the content (cards behind the front one
// are squeezed to its height while stacked) plus padding and border.
const CHROME = 34
const heights = reactive<Record<number, number>>({})
const ro = new ResizeObserver((es) => {
  for (const e of es) {
    const id = Number((e.target as HTMLElement).dataset.id)
    heights[id] = (e.target as HTMLElement).offsetHeight + CHROME
  }
})
onBeforeUnmount(() => ro.disconnect())
function track(el: unknown, id: number) {
  if (!(el instanceof HTMLElement)) return
  el.dataset.id = String(id)
  heights[id] = el.offsetHeight + CHROME
  ro.observe(el)
}

const ordered = computed(() => [...toast.items].reverse()) // newest first
const frontH = computed(() => (ordered.value[0] ? heights[ordered.value[0].id] ?? 72 : 0))

function style(t: Toast, i: number) {
  if (expanded.value) {
    let y = 0
    for (let k = 0; k < i; k++) y += (heights[ordered.value[k].id] ?? 72) + GAP
    return { transform: `translateY(${y}px)`, zIndex: 100 - i, opacity: 1 }
  }
  const shown = i < VISIBLE
  return {
    transform: `translateY(${i * PEEK}px) scale(${1 - i * 0.05})`,
    zIndex: 100 - i,
    opacity: shown ? 1 : 0,
    // Older cards take the front card's height so only their bottom edge shows.
    height: i > 0 ? `${frontH.value}px` : undefined,
    pointerEvents: (shown ? 'auto' : 'none') as 'auto' | 'none',
  }
}
const boxHeight = computed(() => {
  const n = ordered.value.length
  if (!n) return 0
  if (expanded.value) return ordered.value.reduce((a, t) => a + (heights[t.id] ?? 72), 0) + GAP * (n - 1)
  return frontH.value + PEEK * (Math.min(n, VISIBLE) - 1)
})

function enter() { expanded.value = true; toast.pause() }
function leave() { expanded.value = false; toast.resume() }

function go(t: Toast) {
  if (!t.action) return
  router.push({ path: t.action.route, query: t.action.focus ? { focus: t.action.focus } : {} })
  toast.dismiss(t.id)
}
function copyId(t: Toast) { if (t.requestId) clipCopy(t.requestId).catch(() => {}) }
</script>

<template>
  <section
    class="toasts fixed right-4 top-4 z-[60] w-[min(calc(100vw-2rem),380px)]"
    :style="{ height: boxHeight + 'px' }"
    :aria-label="$t('toast.region')"
    @mouseenter="enter" @mouseleave="leave" @focusin="enter" @focusout="leave"
  >
    <TransitionGroup name="toast">
      <div
        v-for="(t, i) in ordered" :key="t.id"
        class="toast-slot absolute inset-x-0 top-0 origin-top"
        :style="style(t, i)"
      >
        <div
          class="toast-card" :class="`toast-${t.kind}`"
          :role="t.kind === 'error' || t.kind === 'warn' ? 'alert' : 'status'"
        >
          <!-- Content of older cards is hidden while stacked: only their edge shows. -->
          <div :ref="(el) => track(el, t.id)" class="flex items-start gap-3 transition-opacity duration-200" :class="!expanded && i > 0 ? 'opacity-0' : ''">
            <component :is="icons[t.kind]" class="toast-accent mt-0.5 h-5 w-5 shrink-0" :stroke-width="2" />
            <div class="min-w-0 flex-1">
              <p class="toast-accent text-[15px] font-semibold leading-6">{{ t.title }}</p>
              <p v-if="t.text" class="mt-1 break-words text-sm leading-6 text-[var(--text)]">{{ t.text }}</p>
              <div v-if="t.action || t.requestId" class="mt-2.5 flex flex-wrap gap-2">
                <button v-if="t.action" class="btn btn-primary btn-sm" @click="go(t)">{{ actionLabel(t.action) }}</button>
                <button v-if="t.requestId" class="btn btn-sm" :title="$t('toast.copyIdHint')" @click="copyId(t)">{{ $t('toast.copyId') }}</button>
              </div>
            </div>
            <button class="toast-accent -mr-1 -mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md opacity-80 hover:opacity-100" :aria-label="$t('common.close')" @click="toast.dismiss(t.id)">
              <X class="h-4 w-4" />
            </button>
          </div>
        </div>
      </div>
    </TransitionGroup>
  </section>
</template>

<style scoped>
.toasts { transition: height 0.3s ease; pointer-events: none; }
.toast-slot { transition: transform 0.35s cubic-bezier(0.2, 0.8, 0.2, 1), opacity 0.3s ease, height 0.3s ease; }
.toast-card {
  pointer-events: auto;
  height: 100%;
  overflow: hidden;
  border-radius: 0.75rem;
  border: 1px solid var(--tc-border);
  background: var(--tc-bg);
  padding: 1rem 1rem 1rem 1.1rem;
  box-shadow: 0 8px 24px -6px rgb(15 23 42 / 0.14), 0 2px 6px -2px rgb(15 23 42 / 0.08);
}
.toast-accent { color: var(--tc-accent); }
/* Tinted on the surface colour, so it stays opaque in both themes. */
.toast-success { --tc: #16a34a; --tc-accent: #15803d; }
.toast-info { --tc: #3b82f6; --tc-accent: #1d4ed8; }
.toast-warn { --tc: #f59e0b; --tc-accent: #b45309; }
.toast-error { --tc: #ef4444; --tc-accent: #b91c1c; }
.toast-card { --tc-bg: color-mix(in srgb, var(--tc) 7%, var(--surface)); --tc-border: color-mix(in srgb, var(--tc) 32%, var(--surface)); }
/* Whole selector in :global(): Vue drops whatever follows a partial :global(). */
:global([data-theme="dark"] .toast-card) { --tc-bg: color-mix(in srgb, var(--tc) 14%, var(--surface)); --tc-border: color-mix(in srgb, var(--tc) 40%, var(--surface)); }
:global([data-theme="dark"] .toast-success) { --tc-accent: #86efac; }
:global([data-theme="dark"] .toast-info) { --tc-accent: #93c5fd; }
:global([data-theme="dark"] .toast-warn) { --tc-accent: #fcd34d; }
:global([data-theme="dark"] .toast-error) { --tc-accent: #fca5a5; }

/* New toasts drop in from above; dismissed ones fade out to the right. */
.toast-enter-from { opacity: 0 !important; transform: translateY(-24px) scale(0.98) !important; }
.toast-leave-to { opacity: 0 !important; transform: translateX(40px) !important; }
.toast-leave-active { transition: transform 0.25s ease, opacity 0.2s ease; }
@media (prefers-reduced-motion: reduce) {
  .toasts, .toast-slot, .toast-leave-active { transition: none; }
}
</style>
