<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, ChevronUp, Pause, Play, RotateCcw, RotateCw, Volume2, VolumeX } from 'lucide-vue-next'
import { DropdownMenuContent, DropdownMenuPortal, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuRoot, DropdownMenuTrigger } from 'reka-ui'
import { tint, useTheme } from '@/theme/theme'

// Audio preview: custom controls over a live spectrum. The spectrum taps the
// <audio> element through Web Audio (AnalyserNode); the context is created on
// the first play, which is a user gesture, so playback is never left muted by
// a suspended context. If Web Audio is unavailable the audio still plays and
// the canvas shows a flat baseline.
// mini: the compact bar used while minimized (same <audio>, so playback goes on).
const props = defineProps<{ src: string; name: string; mini?: boolean }>()
const { t } = useI18n()
const theme = useTheme()

const audio = ref<HTMLAudioElement>()
const canvas = ref<HTMLCanvasElement>()
const playing = ref(false)
const current = ref(0)
const duration = ref(0)
const buffered = ref(0)
const volume = ref(1)
const muted = ref(false)
const rate = ref(1)
const error = ref(false)
const RATES = [0.5, 0.75, 1, 1.25, 1.5, 2, 3, 5]

// Volume, mute and speed are remembered per browser.
const PREF_KEY = 'ec.audio.prefs'
try {
  const p = JSON.parse(localStorage.getItem(PREF_KEY) ?? '{}')
  if (typeof p.volume === 'number') volume.value = Math.min(1, Math.max(0, p.volume))
  if (typeof p.muted === 'boolean') muted.value = p.muted
  if (typeof p.rate === 'number' && RATES.includes(p.rate)) rate.value = p.rate
} catch { /* private mode */ }
watch([volume, muted, rate], () => {
  const a = audio.value
  if (a) { a.volume = volume.value; a.muted = muted.value; a.playbackRate = rate.value }
  try { localStorage.setItem(PREF_KEY, JSON.stringify({ volume: volume.value, muted: muted.value, rate: rate.value })) } catch { /* ignore */ }
})

let ctx: AudioContext | undefined
let analyser: AnalyserNode | undefined
let freq: Uint8Array<ArrayBuffer> | undefined
// Spectrum source. Preferred: a side tap with captureStream() (Chromium) —
// the <audio> element keeps playing through its own output, untouched, and
// the analyser only listens. Routing playback through Web Audio instead
// (createMediaElementSource) adds a buffer to the audible path: after a seek
// the browser still plays out audio decoded before it (~40 ms in Chrome,
// clearly audible in Safari), heard as the old passage repeating. That route
// is kept only where captureStream is missing (Safari, Firefox — whose
// mozCaptureStream would silence the element), with a gate that mutes across
// each seek until the stale audio has drained.
//
// Routed playback is heard only through the context, so the element is
// connected only once the context is confirmed running; a context the browser
// keeps suspended (no user gesture) is dropped and the audio plays directly.
let gate: GainNode | undefined
async function ensureGraph() {
  const el = audio.value
  if (!el) return
  if (ctx) { if (ctx.state === 'suspended') await ctx.resume().catch(() => {}); return }
  let c: AudioContext | undefined
  try {
    c = new AudioContext()
    if (c.state !== 'running') await Promise.race([c.resume(), new Promise((r) => setTimeout(r, 300))])
    if (c.state !== 'running') { c.close().catch(() => {}); return }
    const an = c.createAnalyser()
    an.fftSize = 2048
    an.smoothingTimeConstant = 0.78
    const capture = (el as HTMLAudioElement & { captureStream?: () => MediaStream }).captureStream
    if (typeof capture === 'function') {
      c.createMediaStreamSource(capture.call(el)).connect(an)
      const sink = c.createGain() // keeps the analyser pulled; adds nothing audible
      sink.gain.value = 0
      an.connect(sink).connect(c.destination)
    } else {
      gate = c.createGain()
      c.createMediaElementSource(el).connect(an)
      an.connect(gate).connect(c.destination)
    }
    analyser = an
    freq = new Uint8Array(an.frequencyBinCount)
    ctx = c
  } catch { analyser = undefined; gate = undefined; c?.close().catch(() => {}) } // no spectrum, plain playback
}

// Seek gate (routed playback only): silent from "seeking" until a short while
// after "seeked", then a quick fade-in, so the stale audio is never heard.
const STALE_MS = 180
let gateTimer: number | undefined
function onSeeking() {
  if (!gate || !ctx) return
  clearTimeout(gateTimer)
  gate.gain.cancelScheduledValues(ctx.currentTime)
  gate.gain.setValueAtTime(0, ctx.currentTime)
}
function onSeeked() {
  if (!gate || !ctx) return
  clearTimeout(gateTimer)
  gateTimer = window.setTimeout(() => {
    if (!gate || !ctx) return
    const now = ctx.currentTime
    gate.gain.cancelScheduledValues(now)
    gate.gain.setValueAtTime(0, now)
    gate.gain.linearRampToValueAtTime(1, now + 0.04)
  }, STALE_MS)
}

// Opening a track plays it. Browsers may refuse playback with sound that does
// not follow a click closely enough; then it stays paused on the play button.
async function autoplay() {
  const a = audio.value
  if (!a) return
  const active = (navigator as Navigator & { userActivation?: { isActive: boolean } }).userActivation?.isActive ?? true
  if (active) await ensureGraph()
  a.play().catch(() => { /* refused: the user presses play */ })
}

async function toggle() {
  const a = audio.value
  if (!a) return
  if (a.paused) { await ensureGraph(); a.play().catch(() => {}) } else a.pause()
}
// Skip step of the ⟲/⟳ buttons, the system media controls and the arrow keys.
const SKIP = 3
// Every seek goes through here: the gate closes synchronously, before the
// "seeking" event (dispatched asynchronously) would.
function seekAt(time: number) {
  const a = audio.value
  if (!a) return
  onSeeking()
  a.currentTime = time
}
function seekBy(d: number) { const a = audio.value; if (a) seekAt(Math.min(Math.max(0, a.currentTime + d), duration.value || a.currentTime + d)) }
function seekTo(e: Event) { seekAt(Number((e.target as HTMLInputElement).value)) }

// Read the element from the event: the ref is already cleared when the
// element fires its last events while the player unmounts.
function onTime(e: Event) {
  const a = e.target as HTMLAudioElement
  current.value = a.currentTime
  if (a.buffered.length) buffered.value = a.buffered.end(a.buffered.length - 1)
}
function onMeta(e: Event) {
  const a = e.target as HTMLAudioElement
  duration.value = Number.isFinite(a.duration) ? a.duration : 0
  a.volume = volume.value; a.muted = muted.value; a.playbackRate = rate.value
}
watch(() => props.src, () => { playing.value = false; current.value = 0; duration.value = 0; buffered.value = 0; error.value = false })

const fmt = (s: number) => {
  if (!Number.isFinite(s) || s < 0) s = 0
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), x = Math.floor(s % 60)
  return (h ? `${h}:${String(m).padStart(2, '0')}` : String(m).padStart(2, '0')) + ':' + String(x).padStart(2, '0')
}
const pct = computed(() => (duration.value ? (current.value / duration.value) * 100 : 0))
const bufPct = computed(() => (duration.value ? (buffered.value / duration.value) * 100 : 0))

// Spectrum: log-spaced bars (bass gets as much room as treble), mirrored
// around a centre line, easing to rest when paused.
const BARS = 72
const levels = new Float32Array(BARS)
let raf = 0
const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
function bands(): number[] {
  const out = new Array(BARS).fill(0)
  if (!analyser || !freq || !playing.value) return out
  analyser.getByteFrequencyData(freq)
  const n = freq.length, lo = 2, hi = Math.floor(n * 0.75) // skip DC and the near-empty top
  for (let i = 0; i < BARS; i++) {
    const a = Math.floor(lo * Math.pow(hi / lo, i / BARS))
    const b = Math.max(a + 1, Math.floor(lo * Math.pow(hi / lo, (i + 1) / BARS)))
    let peak = 0
    for (let j = a; j < b && j < n; j++) peak = Math.max(peak, freq[j])
    out[i] = peak / 255
  }
  return out
}
function draw() {
  raf = 0
  const c = canvas.value
  if (!c || props.mini) return
  const dpr = window.devicePixelRatio || 1
  const w = c.clientWidth, h = c.clientHeight
  if (c.width !== Math.round(w * dpr) || c.height !== Math.round(h * dpr)) { c.width = Math.round(w * dpr); c.height = Math.round(h * dpr) }
  const g = c.getContext('2d')!
  g.setTransform(dpr, 0, 0, dpr, 0, 0)
  g.clearRect(0, 0, w, h)
  const target = bands()
  let moving = false
  for (let i = 0; i < BARS; i++) {
    const v = target[i]
    // fast attack, slower release
    levels[i] += (v - levels[i]) * (v > levels[i] ? 0.6 : 0.18)
    if (levels[i] > 0.004) moving = true
  }
  const accent = theme.dark.value ? tint(theme.state.accent, 0.25) : theme.state.accent
  const gap = 2, bw = Math.max(1, (w - gap * (BARS - 1)) / BARS), mid = h / 2
  const grad = g.createLinearGradient(0, 0, 0, h)
  grad.addColorStop(0, tint(accent, 0.45))
  grad.addColorStop(0.5, accent)
  grad.addColorStop(1, tint(accent, 0.45))
  g.fillStyle = grad
  for (let i = 0; i < BARS; i++) {
    const bh = Math.max(2, levels[i] * (h * 0.92))
    const x = i * (bw + gap)
    g.globalAlpha = 0.35 + 0.65 * Math.min(1, levels[i] * 1.6)
    g.beginPath()
    g.roundRect(x, mid - bh / 2, bw, bh, Math.min(bw / 2, 3))
    g.fill()
  }
  g.globalAlpha = 1
  // Progress line along the centre, so the spectrum doubles as a position cue.
  g.fillStyle = accent
  g.globalAlpha = 0.25
  g.fillRect(0, mid - 0.5, w, 1)
  g.globalAlpha = 1
  if ((playing.value && !reduceMotion) || moving) raf = requestAnimationFrame(draw)
}
function kick() { if (!raf && !props.mini) raf = requestAnimationFrame(draw) }
watch(playing, kick)
watch(() => props.mini, (m) => { if (m) { cancelAnimationFrame(raf); clearTimeout(gateTimer); raf = 0 } else kick() })

// Media Session: title on the lock screen / system media controls, and the
// hardware media keys work while the tab is in the background.
function setSession() {
  const ms = navigator.mediaSession
  if (!ms) return
  try {
    ms.metadata = new MediaMetadata({ title: props.name, artist: 'EasyCloud' })
    ms.setActionHandler('play', () => { toggle() })
    ms.setActionHandler('pause', () => audio.value?.pause())
    ms.setActionHandler('seekbackward', () => seekBy(-SKIP))
    ms.setActionHandler('seekforward', () => seekBy(SKIP))
    ms.setActionHandler('seekto', (d) => { if (d.seekTime !== undefined) seekAt(d.seekTime) })
  } catch { /* unsupported action */ }
}
watch(playing, (p) => {
  if (p) setSession()
  if (navigator.mediaSession) navigator.mediaSession.playbackState = p ? 'playing' : 'paused'
})
watch(() => props.name, () => { if (playing.value) setSession() })
watch(() => [theme.dark.value, theme.state.accent], kick)
let ro: ResizeObserver | undefined
onMounted(() => { ro = new ResizeObserver(kick); if (canvas.value) ro.observe(canvas.value); kick(); autoplay() })
onBeforeUnmount(() => {
  if (navigator.mediaSession) {
    navigator.mediaSession.metadata = null
    for (const a of ['play', 'pause', 'seekbackward', 'seekforward', 'seekto'] as MediaSessionAction[]) { try { navigator.mediaSession.setActionHandler(a, null) } catch { /* ignore */ } }
  }
  cancelAnimationFrame(raf); ro?.disconnect(); audio.value?.pause(); ctx?.close().catch(() => {}) })

function onKey(e: KeyboardEvent) {
  if ((e.target as HTMLElement).tagName === 'INPUT' && (e.target as HTMLInputElement).type !== 'range') return
  const k = e.key.toLowerCase()
  if (k === ' ' || k === 'k') { e.preventDefault(); toggle() }
  else if (k === 'arrowleft' || k === 'j') { e.preventDefault(); seekBy(k === 'j' ? -15 : -SKIP) }
  else if (k === 'arrowright' || k === 'l') { e.preventDefault(); seekBy(k === 'l' ? 15 : SKIP) }
  else if (k === 'm') muted.value = !muted.value
}
</script>

<template>
  <div class="w-full select-none outline-none" :class="mini ? '' : 'rounded-xl bg-[var(--surface-sunken)] p-4 sm:p-5'" tabindex="0" @keydown="onKey">
    <audio
      ref="audio" :src="src" preload="metadata"
      @play="playing = true" @pause="playing = false" @ended="playing = false" @seeking="onSeeking" @seeked="onSeeked"
      @timeupdate="onTime" @progress="onTime" @loadedmetadata="onMeta" @durationchange="onMeta" @error="error = true"
    />
    <!-- Minimized: play/pause and a thin progress bar. -->
    <div v-if="mini" class="flex items-center gap-2">
      <button class="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-brand-600 text-white hover:bg-brand-700" :aria-label="playing ? t('player.pause') : t('player.play')" @click.stop="toggle">
        <Pause v-if="playing" class="h-3.5 w-3.5" fill="currentColor" />
        <Play v-else class="ml-0.5 h-3.5 w-3.5" fill="currentColor" />
      </button>
      <div class="min-w-0 flex-1">
        <slot name="title" />
        <div class="mt-1 flex items-center gap-2 text-[10px] tabular-nums text-[var(--text-muted)]">
          <div class="relative h-1 flex-1 overflow-hidden rounded-full bg-[var(--border)]">
            <div class="absolute inset-y-0 left-0 bg-brand-600" :style="{ width: pct + '%' }" />
          </div>
          <span>{{ fmt(current) }} / {{ fmt(duration) }}</span>
        </div>
      </div>
    </div>

    <template v-else>
    <div class="relative">
      <canvas ref="canvas" class="block h-32 w-full cursor-pointer sm:h-40" :aria-label="t('player.spectrum')" role="img" @click="toggle" />
      <p v-if="error" class="absolute inset-0 flex items-center justify-center text-center text-sm text-red-600">{{ t('player.errorAudio') }}</p>
    </div>

    <!-- Progress -->
    <div class="mt-3 flex items-center gap-3 text-xs tabular-nums text-[var(--text-muted)]">
      <span class="w-12 text-right">{{ fmt(current) }}</span>
      <div class="relative h-4 flex-1">
        <div class="absolute inset-x-0 top-1/2 h-1 -translate-y-1/2 overflow-hidden rounded-full bg-[var(--border)]">
          <div class="absolute inset-y-0 left-0 bg-[var(--text-muted)] opacity-25" :style="{ width: bufPct + '%' }" />
          <div class="absolute inset-y-0 left-0 bg-brand-600" :style="{ width: pct + '%' }" />
        </div>
        <input
          type="range" min="0" :max="duration || 0" step="0.1" :value="current" :aria-label="t('player.progress')"
          class="audio-range absolute inset-0 w-full cursor-pointer opacity-0" @input="seekTo"
        />
        <div class="pointer-events-none absolute top-1/2 h-3 w-3 -translate-x-1/2 -translate-y-1/2 rounded-full bg-brand-600 shadow ring-2 ring-[var(--surface)]" :style="{ left: pct + '%' }" />
      </div>
      <span class="w-12">{{ fmt(duration) }}</span>
    </div>

    <!-- Controls -->
    <div class="mt-3 flex items-center gap-2">
      <div class="flex flex-1 items-center gap-1">
        <!-- Speed: a menu of choices (above the button: the player sits low on small screens). -->
        <DropdownMenuRoot :modal="false">
          <DropdownMenuTrigger
            class="flex h-8 items-center gap-1 rounded-md px-2 text-xs tabular-nums hover:bg-[var(--surface-muted)] hover:text-[var(--text)] data-[state=open]:bg-[var(--surface-muted)]"
            :class="rate === 1 ? 'text-[var(--text-muted)]' : 'font-medium text-brand-600'"
            :title="t('player.speed')" :aria-label="t('player.speed')"
          >{{ rate === 1 ? t('player.speedShort') : rate + '×' }}<ChevronUp class="h-3 w-3 opacity-60" /></DropdownMenuTrigger>
          <DropdownMenuPortal>
            <DropdownMenuContent side="top" align="start" :side-offset="6" class="z-[70] min-w-28 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-1 shadow-[var(--shadow-pop)]">
              <DropdownMenuRadioGroup :model-value="String(rate)" @update:model-value="(v) => (rate = Number(v))">
                <DropdownMenuRadioItem
                  v-for="r in [...RATES].reverse()" :key="r" :value="String(r)"
                  class="flex cursor-default items-center gap-2 rounded-md py-1.5 pl-2 pr-3 text-[13px] tabular-nums outline-none data-[highlighted]:bg-[var(--surface-muted)] data-[state=checked]:font-medium data-[state=checked]:text-brand-600"
                >
                  <span class="flex w-3.5 justify-center"><Check v-if="r === rate" class="h-3.5 w-3.5" /></span>{{ r === 1 ? t('player.normal') : r + '×' }}
                </DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenuPortal>
        </DropdownMenuRoot>
      </div>
      <button class="relative flex h-9 w-9 items-center justify-center rounded-full text-[var(--text-muted)] hover:bg-[var(--surface-muted)] hover:text-[var(--text)]" :title="t('player.back', { n: SKIP })" :aria-label="t('player.back', { n: SKIP })" @click="seekBy(-SKIP)">
        <RotateCcw class="h-5 w-5" /><span class="absolute text-[8px] font-semibold">{{ SKIP }}</span>
      </button>
      <button class="flex h-12 w-12 items-center justify-center rounded-full bg-brand-600 text-white shadow hover:bg-brand-700" :aria-label="playing ? t('player.pause') : t('player.play')" @click="toggle">
        <Pause v-if="playing" class="h-5 w-5" fill="currentColor" />
        <Play v-else class="ml-0.5 h-5 w-5" fill="currentColor" />
      </button>
      <button class="relative flex h-9 w-9 items-center justify-center rounded-full text-[var(--text-muted)] hover:bg-[var(--surface-muted)] hover:text-[var(--text)]" :title="t('player.forward', { n: SKIP })" :aria-label="t('player.forward', { n: SKIP })" @click="seekBy(SKIP)">
        <RotateCw class="h-5 w-5" /><span class="absolute text-[8px] font-semibold">{{ SKIP }}</span>
      </button>
      <div class="flex flex-1 items-center justify-end gap-1.5">
        <button class="flex h-8 w-8 items-center justify-center rounded-md text-[var(--text-muted)] hover:bg-[var(--surface-muted)] hover:text-[var(--text)]" :aria-label="muted ? t('player.unmute') : t('player.mute')" @click="muted = !muted">
          <VolumeX v-if="muted || volume === 0" class="h-4 w-4" /><Volume2 v-else class="h-4 w-4" />
        </button>
        <input v-model.number="volume" type="range" min="0" max="1" step="0.01" class="hidden w-20 accent-[var(--color-brand-600)] sm:block" :aria-label="t('player.volume')" @input="muted = false" />
      </div>
    </div>
    </template>
  </div>
</template>
