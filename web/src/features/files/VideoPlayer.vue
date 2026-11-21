<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  Airplay, Cast, Copy, Download, FileVideo, Keyboard, LoaderCircle, Maximize, Maximize2, Minimize, Minus, PictureInPicture2,
  Check, Pause, Play, Repeat, RotateCcw, Save, Square, TriangleAlert, Volume1, Volume2, VolumeX, X,
} from 'lucide-vue-next'
import { filesApi, type FileEntry } from '@/api/files'
import { useModalLayer } from '@/components/modalStack'
import { useTasks } from '@/stores/tasks'
import { useToast } from '@/stores/toast'

// Video viewer. The picture fills the window; the title bar and the control bar
// float over it and fade out when the pointer leaves or rests (a "clean screen").
// Menus render inline, never in a portal, so they still show in fullscreen.
const props = defineProps<{ entry: FileEntry | null }>()
const emit = defineEmits<{ close: []; download: [e: FileEntry] }>()
const { t } = useI18n()

const open = computed(() => !!props.entry)
// The server says whether a video needs a playback fix (e.g. B-frames without
// composition offsets, which browsers present out of order) and then streams a
// fixed virtual copy; the file itself is never changed. If the fixed stream
// fails for any reason, the player falls back to the original once.
const src = ref('')
const probing = ref(false)
const fixes = ref<string[]>([])
let probeSeq = 0
async function choose(path: string) {
  const seq = ++probeSeq
  probing.value = true; src.value = ''; fixes.value = []
  let fixed = false
  try {
    const r = await filesApi.playback(path)
    fixed = r.fixed
    if (seq === probeSeq) fixes.value = r.reasons
  } catch { /* analysis unavailable: play the original */ }
  if (seq !== probeSeq) return
  src.value = fixed ? filesApi.streamUrl(path) : filesApi.downloadUrl(path, true)
  probing.value = false
}
const fixTip = computed(() => fixes.value.map((r) => t(`player.fix.${r}`)).join(' '))

// Save the fixed version next to the original as "<name>-fix.<ext>" (a server
// task: the copy can be gigabytes; progress shows in the task panel).
const tasks = useTasks()
const toast = useToast()
// Save state per video path: 'saving' until the task ends, then 'saved'; a
// failed task clears it so the button can be used again.
const saveState = ref<Record<string, 'saving' | 'saved'>>({})
const offTask = tasks.onFinished((tk) => {
  const p = tk.kind === 'videofix' ? tk.params.sources[0] : ''
  if (!p || !saveState.value[p]) return
  if (tk.status === 'done') saveState.value = { ...saveState.value, [p]: 'saved' }
  else { const { [p]: _, ...rest } = saveState.value; saveState.value = rest }
})
onBeforeUnmount(offTask)
const saveLabel = computed(() => {
  const st = props.entry ? saveState.value[props.entry.path] : undefined
  return st === 'saving' ? t('player.saving') : st === 'saved' ? t('player.saved') : t('player.saveFixed')
})
const fixedName = computed(() => {
  const n = props.entry?.name ?? ''
  const dot = n.lastIndexOf('.')
  return dot > 0 ? `${n.slice(0, dot)}-fix${n.slice(dot)}` : `${n}-fix`
})
async function saveFixed() {
  const e = props.entry
  if (!e) return
  try {
    saveState.value = { ...saveState.value, [e.path]: 'saving' }
    await tasks.create('videofix', [e.path])
    toast.info(t('toast.videoSaving'), t('toast.videoSavingD', { name: fixedName.value }))
  } catch (err) {
    const { [e.path]: _, ...rest } = saveState.value
    saveState.value = rest
    toast.error(err)
  }
}

// Window states: centred, maximized (fills the viewport) or minimized (a small
// player in the corner without the backdrop, so the page stays usable). Only
// the frame's classes change between them: the <video> element is never
// recreated, so playback continues.
const mini = ref(false)
const titleId = 'video-player-title'

const layer = useModalLayer() // only the full-size player is a modal layer
watch(() => open.value && !mini.value, (o) => (o ? layer.push() : layer.pop()), { immediate: true })
onBeforeUnmount(layer.pop)
const z = computed(() => 40 + Math.max(0, layer.depth.value) * 10)

const stage = ref<HTMLElement>()
const video = ref<HTMLVideoElement>()
const bar = ref<HTMLElement>()

// ---------- playback state ----------
const paused = ref(true)
const waiting = ref(true)
const ended = ref(false)
const current = ref(0)
const duration = ref(0)
const buffered = ref(0)
const volume = ref(1)
const muted = ref(false)
const rate = ref(1)
const loop = ref(false)
const error = ref<'codec' | 'network' | ''>('')

// Volume, mute and speed are remembered per browser.
const PREF_KEY = 'ec.player'
function loadPrefs() {
  try {
    const p = JSON.parse(localStorage.getItem(PREF_KEY) ?? '{}')
    if (typeof p.volume === 'number') volume.value = Math.min(1, Math.max(0, p.volume))
    if (typeof p.muted === 'boolean') muted.value = p.muted
    if (typeof p.rate === 'number' && RATES.includes(p.rate)) rate.value = p.rate
  } catch { /* defaults */ }
}
function savePrefs() {
  try { localStorage.setItem(PREF_KEY, JSON.stringify({ volume: volume.value, muted: muted.value, rate: rate.value })) } catch { /* private mode */ }
}

// Where each video was left off (most recent 100), to resume on reopen.
const POS_KEY = 'ec.player.pos'
function positions(): Record<string, number> {
  try { return JSON.parse(localStorage.getItem(POS_KEY) ?? '{}') } catch { return {} }
}
function savePosition() {
  const e = props.entry, v = video.value
  if (!e || !v || !duration.value) return
  const all = positions()
  delete all[e.path]
  // Nothing worth resuming near either end.
  if (v.currentTime > 10 && v.currentTime < duration.value - 10) all[e.path] = Math.floor(v.currentTime)
  const keys = Object.keys(all)
  for (const k of keys.slice(0, Math.max(0, keys.length - 100))) delete all[k]
  try { localStorage.setItem(POS_KEY, JSON.stringify(all)) } catch { /* private mode */ }
}

const RATES = [0.5, 0.75, 1, 1.25, 1.5, 2, 3, 5]
const SHORTCUTS: [string, string][] = [
  ['Space / K', 'player.keys.play'], ['← →', 'player.keys.seek5'], ['J  L', 'player.keys.seek10'], ['↑ ↓', 'player.keys.volume'],
  ['M', 'player.keys.mute'], ['F', 'player.keys.fullscreen'], ['<  >', 'player.keys.speed'], ['0 – 9', 'player.keys.jump'], ['Esc', 'player.keys.esc'],
]

function onLoaded() {
  const v = video.value!
  duration.value = v.duration || 0
  v.volume = volume.value
  v.muted = muted.value
  v.playbackRate = rate.value
  const at = props.entry ? positions()[props.entry.path] : undefined
  if (at && at < duration.value - 10) {
    v.currentTime = at
    flash(t('player.resumed', { t: fmt(at) }))
  }
  if ('mediaSession' in navigator && props.entry) navigator.mediaSession.metadata = new MediaMetadata({ title: props.entry.name })
  autoplay(v)
}

// Opening a video plays it. Browsers may refuse playback with sound that is
// not tied to a click (Safari by default): then it plays muted with an
// "unmute" button, and that automatic mute is not saved as a preference.
const autoMuted = ref(false)
function autoplay(v: HTMLVideoElement) {
  v.play().catch((e: DOMException) => {
    if (e?.name !== 'NotAllowedError' || v.muted) return
    autoMuted.value = true
    v.muted = true
    v.play().catch(() => { autoMuted.value = false; v.muted = muted.value })
  })
}
function unmuteAuto() {
  const v = video.value
  autoMuted.value = false
  if (!v) return
  v.muted = false
  if (v.volume === 0) v.volume = 0.5
}
let lastSave = 0
function onTime() {
  const v = video.value!
  if (!dragging.value) current.value = v.currentTime
  if (Date.now() - lastSave > 5000) { lastSave = Date.now(); savePosition() }
}
function onProgress() {
  const v = video.value!
  for (let i = v.buffered.length - 1; i >= 0; i--) {
    if (v.buffered.start(i) <= v.currentTime + 0.5) { buffered.value = v.buffered.end(i); return }
  }
}
function onError() {
  if (fixes.value.length && props.entry) { // fixed stream failed: retry with the original
    fixes.value = []
    src.value = filesApi.downloadUrl(props.entry.path, true)
    return
  }
  const code = video.value?.error?.code
  error.value = code === MediaError.MEDIA_ERR_NETWORK ? 'network' : 'codec'
  waiting.value = false
}
function onVolume() {
  const v = video.value!
  if (autoMuted.value && v.muted) return // the automatic mute is not the user's choice
  autoMuted.value = false
  volume.value = v.volume
  muted.value = v.muted
  savePrefs()
}

// ---------- actions ----------
function togglePlay() {
  const v = video.value
  if (!v || error.value) return
  if (v.paused || v.ended) v.play().catch(() => { /* autoplay refused: stay paused */ })
  else v.pause()
}
function seek(to: number) {
  const v = video.value
  if (!v || !duration.value) return
  v.currentTime = Math.min(duration.value, Math.max(0, to))
  current.value = v.currentTime
}
function seekBy(d: number) {
  seek(current.value + d)
  flash(d > 0 ? t('player.forward', { n: d }) : t('player.back', { n: -d }))
}
function setVolume(x: number) {
  const v = video.value
  if (!v) return
  v.volume = Math.min(1, Math.max(0, Math.round(x * 100) / 100))
  v.muted = v.volume === 0
}
function toggleMute() {
  const v = video.value
  if (!v) return
  if (v.muted && v.volume === 0) v.volume = 0.5
  v.muted = !v.muted
}
function setRate(r: number) {
  rate.value = r
  if (video.value) video.value.playbackRate = r
  savePrefs()
  flash(r === 1 ? t('player.normalSpeed') : t('player.speedIs', { n: r }))
}
function close() {
  savePosition()
  mini.value = false
  emit('close')
}
// Double-click: fullscreen for the full player, back to full size for the mini one.
function onDblClick() {
  if (mini.value) mini.value = false
  else toggleFullscreen()
}

// ---------- window, fullscreen, picture-in-picture, casting ----------
const maximized = ref(false)
const fullscreen = ref(false)
const pipSupported = typeof document !== 'undefined' && 'pictureInPictureEnabled' in document && document.pictureInPictureEnabled
const castAvailable = ref(false)
const castKind = ref<'airplay' | 'cast'>('cast')

function onFsChange() { fullscreen.value = !!stage.value && document.fullscreenElement === stage.value }
function toggleFullscreen() {
  if (document.fullscreenElement) { document.exitFullscreen().catch(() => {}); return }
  const el = stage.value as (HTMLElement & { webkitRequestFullscreen?: () => void }) | undefined
  if (el?.requestFullscreen) el.requestFullscreen().catch(() => {})
  else (video.value as HTMLVideoElement & { webkitEnterFullscreen?: () => void })?.webkitEnterFullscreen?.() // iOS Safari
}
// Native picture-in-picture (where supported). The page-side player shrinks to
// the corner meanwhile instead of leaving a large black "playing elsewhere" box.
async function pip() {
  const v = video.value
  if (!v) return
  try {
    if (document.pictureInPictureElement) await document.exitPictureInPicture()
    else { await v.requestPictureInPicture(); mini.value = true }
  } catch { /* not allowed right now */ }
}
type CastVideo = HTMLVideoElement & {
  remote?: { watchAvailability(cb: (a: boolean) => void): Promise<number>; prompt(): Promise<void> }
  webkitShowPlaybackTargetPicker?: () => void
}
function watchCast(v: CastVideo) {
  if ('WebKitPlaybackTargetAvailabilityEvent' in window) {
    castKind.value = 'airplay'
    v.addEventListener('webkitplaybacktargetavailabilitychanged', (e) => {
      castAvailable.value = (e as Event & { availability?: string }).availability === 'available'
    })
  } else if (v.remote) {
    v.remote.watchAvailability((a) => (castAvailable.value = a)).catch(() => (castAvailable.value = false))
  }
}
function cast() {
  const v = video.value as CastVideo | undefined
  if (!v) return
  if (v.webkitShowPlaybackTargetPicker) v.webkitShowPlaybackTargetPicker()
  else v.remote?.prompt().catch(() => {})
}

// ---------- auto-hiding chrome ----------
const hovering = ref(false)
const idle = ref(false)
const menu = ref<'' | 'speed' | 'keys'>('')
const dragging = ref(false)
// Keyboard use also reveals the bars (e.g. to see where a seek landed), but only
// until the next idle timeout, so a pointer outside the player still means clean.
const keyActive = ref(false)
let idleTimer: number | undefined
let lastPointer = 'mouse'
function poke() {
  idle.value = false
  clearTimeout(idleTimer)
  idleTimer = window.setTimeout(() => { idle.value = true; keyActive.value = false }, 2500)
}
// Shown while the pointer is over the player and moving (or the video is paused);
// hidden as soon as it leaves. Open menus, scrubbing and errors keep it up.
const chrome = computed(() => !!menu.value || dragging.value || !!error.value || (keyActive.value && !idle.value) || (hovering.value && (!idle.value || paused.value)))
function onPointerMove(e: PointerEvent) {
  lastPointer = e.pointerType
  if (e.pointerType === 'mouse') { hovering.value = true; poke() }
}
// A click without movement is activity too (e.g. clicking play while still).
function onPointerDown(e: PointerEvent) {
  lastPointer = e.pointerType
  if (e.pointerType === 'mouse') { hovering.value = true; poke() }
}
function onPointerLeave(e: PointerEvent) {
  if (e.pointerType === 'mouse') hovering.value = false
}
function onStageClick() {
  // On touch screens the first tap only reveals the controls.
  if (lastPointer !== 'mouse' && !chrome.value) { hovering.value = true; poke(); return }
  if (menu.value) { menu.value = ''; return }
  togglePlay()
}

// ---------- progress bar ----------
const hoverX = ref<number | null>(null)
const scrub = ref(0)
const shown = computed(() => (dragging.value ? scrub.value : current.value))
const pct = (s: number) => (duration.value ? `${Math.min(100, (s / duration.value) * 100)}%` : '0%')
function fracAt(e: PointerEvent) {
  const r = bar.value!.getBoundingClientRect()
  return Math.min(1, Math.max(0, (e.clientX - r.left) / r.width))
}
function barDown(e: PointerEvent) {
  if (!duration.value) return
  bar.value!.setPointerCapture(e.pointerId)
  dragging.value = true
  scrub.value = fracAt(e) * duration.value
}
function barMove(e: PointerEvent) {
  hoverX.value = fracAt(e)
  if (dragging.value) scrub.value = fracAt(e) * duration.value
}
function barUp() {
  if (!dragging.value) return
  dragging.value = false
  seek(scrub.value)
}

// ---------- on-screen feedback ----------
const osd = ref('')
let osdTimer: number | undefined
function flash(text: string) {
  osd.value = text
  clearTimeout(osdTimer)
  osdTimer = window.setTimeout(() => (osd.value = ''), 1000)
}

// ---------- keyboard ----------
function onKey(e: KeyboardEvent) {
  if ((e.target as HTMLElement).tagName === 'INPUT' || e.metaKey || e.ctrlKey || e.altKey) return
  const v = video.value
  if (!v) return
  const k = e.key.toLowerCase()
  let handled = true
  if (k === ' ' || k === 'k') togglePlay()
  else if (k === 'arrowleft') seekBy(-5)
  else if (k === 'arrowright') seekBy(5)
  else if (k === 'j') seekBy(-10)
  else if (k === 'l') seekBy(10)
  else if (k === 'arrowup') { setVolume(v.volume + 0.1); flash(t('player.volumeIs', { n: Math.round(v.volume * 100) })) }
  else if (k === 'arrowdown') { setVolume(v.volume - 0.1); flash(v.muted ? t('player.muted') : t('player.volumeIs', { n: Math.round(v.volume * 100) })) }
  else if (k === 'm') { toggleMute(); flash(v.muted ? t('player.muted') : t('player.volumeIs', { n: Math.round(v.volume * 100) })) }
  else if (k === 'f') toggleFullscreen()
  else if (e.key === '>' || e.key === '<') {
    const i = RATES.indexOf(rate.value) + (e.key === '>' ? 1 : -1)
    if (i >= 0 && i < RATES.length) setRate(RATES[i])
  } else if (/^[0-9]$/.test(k)) seek((Number(k) / 10) * duration.value)
  else handled = false
  if (handled) { e.preventDefault(); keyActive.value = true; poke() }
}
// Esc first closes a menu or leaves fullscreen (the browser does that), then the
// full-size player. A minimized player stays until closed or expanded.
function onDocKey(e: KeyboardEvent) {
  if (e.key !== 'Escape' || !open.value) return
  if (menu.value) { menu.value = ''; e.preventDefault(); return }
  if (document.fullscreenElement || mini.value) return
  e.preventDefault()
  close()
}
const frame = computed(() => mini.value
  ? 'bottom-4 right-4 aspect-video rounded-xl ring-1 ring-white/15'
  : maximized.value ? 'inset-0' : 'left-1/2 top-1/2 aspect-video -translate-x-1/2 -translate-y-1/2 rounded-xl')
const frameStyle = computed(() => ({
  zIndex: mini.value ? 35 : z.value + 1, // the mini player sits below dialogs (z 40+) opened over the page
  width: mini.value ? 'min(380px, calc(100vw - 2rem))' : maximized.value ? undefined : 'min(94vw, 1120px, calc(86vh * 16 / 9))',
}))

// ---------- lifecycle ----------
watch(video, (v) => { if (v) watchCast(v as CastVideo) })
watch(() => props.entry?.path, (p, old) => {
  if (old) savePosition()
  if (!p) return
  error.value = ''; waiting.value = true; ended.value = false; paused.value = true
  current.value = 0; duration.value = 0; buffered.value = 0; menu.value = ''; osd.value = ''
  hovering.value = true; poke(); mini.value = false
  choose(p)
  nextTick(() => stage.value?.focus())
})
onMounted(() => {
  loadPrefs()
  document.addEventListener('fullscreenchange', onFsChange)
  document.addEventListener('keydown', onDocKey)
})
onBeforeUnmount(() => {
  savePosition()
  document.removeEventListener('fullscreenchange', onFsChange)
  document.removeEventListener('keydown', onDocKey)
  clearTimeout(idleTimer); clearTimeout(osdTimer)
})

function fmt(s: number) {
  if (!Number.isFinite(s) || s < 0) s = 0
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = Math.floor(s % 60)
  const mm = h ? String(m).padStart(2, '0') : String(m)
  return `${h ? h + ':' : ''}${mm}:${String(sec).padStart(2, '0')}`
}
const volIcon = computed(() => (muted.value || volume.value === 0 ? VolumeX : volume.value < 0.5 ? Volume1 : Volume2))
const icon = 'flex h-9 w-9 shrink-0 items-center justify-center rounded-md text-white/90 transition-colors hover:bg-white/15 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/60'
</script>

<template>
  <Teleport to="body">
    <template v-if="open">
      <!-- A plain layer rather than a dialog component: switching between modal
           (full size) and non-modal (minimized) must not remount the <video>.
           Opening animates; closing is instant (no leave animation to wait for). -->
      <div v-if="!mini" class="vp-overlay fixed inset-0 bg-black/70 backdrop-blur-[2px]" :style="{ zIndex: z }" @click="close" />
      <div
        role="dialog" :aria-modal="mini ? undefined : 'true'" :aria-labelledby="titleId"
        class="vp-content fixed overflow-hidden bg-black shadow-[0_32px_80px_-12px_rgb(0_0_0/0.6)] outline-none"
        :class="frame" :style="frameStyle"
      >
          <div
            ref="stage" tabindex="-1"
            class="relative h-full w-full select-none bg-black text-white outline-none"
            :class="{ 'cursor-none': hovering && !chrome }"
            @pointermove="onPointerMove" @pointerdown="onPointerDown" @pointerleave="onPointerLeave" @keydown="onKey"
          >
            <video
              v-if="entry && src" ref="video" :key="entry.path" :src="src" :loop="loop" playsinline preload="metadata"
              class="absolute inset-0 h-full w-full object-contain"
              @click="onStageClick" @dblclick="onDblClick"
              @loadedmetadata="onLoaded" @durationchange="duration = video?.duration || 0" @timeupdate="onTime" @progress="onProgress"
              @play="paused = false; ended = false" @pause="paused = true; savePosition()" @playing="waiting = false"
              @waiting="waiting = true" @canplay="waiting = false" @ended="ended = true; paused = true; savePosition()"
              @volumechange="onVolume" @error="onError"
            />
            <!-- Autoplay had to start muted (browser policy): one click restores sound. -->
            <button
              v-if="autoMuted && !mini"
              class="absolute left-1/2 top-16 z-10 flex -translate-x-1/2 items-center gap-2 rounded-full bg-black/70 px-4 py-2 text-sm text-white shadow-lg ring-1 ring-white/15 backdrop-blur hover:bg-black/85"
              @click.stop="unmuteAuto" @pointerdown.stop
            >
              <VolumeX class="h-4 w-4" />{{ $t('player.tapUnmute') }}
            </button>

            <!-- Centre: buffering, play / replay, or why it cannot play. -->
            <!-- One grid cell: a fading-out button never pushes the message aside. -->
            <div class="pointer-events-none absolute inset-0 grid place-items-center [&>*]:col-start-1 [&>*]:row-start-1">
              <div v-if="error" class="pointer-events-auto flex max-w-sm flex-col items-center gap-3 px-6 text-center">
                <TriangleAlert class="h-10 w-10 text-amber-400" :stroke-width="1.5" />
                <p class="text-sm leading-6 text-white/90">{{ error === 'network' ? $t('player.errorNetwork') : $t('player.errorCodec') }}</p>
                <button class="btn btn-sm" @click="entry && emit('download', entry)"><Download class="h-4 w-4" />{{ $t('common.download') }}</button>
              </div>
              <div v-else-if="probing || (waiting && !paused)" class="flex flex-col items-center gap-3">
                <LoaderCircle class="h-11 w-11 animate-spin text-white/80" :stroke-width="1.5" />
                <p v-if="probing" class="text-xs text-white/60">{{ $t('player.analyzing') }}</p>
              </div>
              <Transition name="fade">
                <button
                  v-if="!error && !probing && !(waiting && !paused) && paused && chrome"
                  class="pointer-events-auto flex h-16 w-16 items-center justify-center rounded-full bg-black/45 backdrop-blur-sm transition-transform hover:scale-105"
                  :aria-label="ended ? $t('player.replay') : $t('player.play')" @click="togglePlay"
                >
                  <RotateCcw v-if="ended" class="h-7 w-7" />
                  <Play v-else class="ml-1 h-7 w-7" fill="currentColor" />
                </button>
              </Transition>
            </div>

            <Transition name="fade">
              <div v-if="osd" class="pointer-events-none absolute left-1/2 top-16 -translate-x-1/2 rounded-full bg-black/60 px-4 py-1.5 text-[13px] tabular-nums backdrop-blur-sm">{{ osd }}</div>
            </Transition>

            <!-- Title bar -->
            <div
              class="absolute inset-x-0 top-0 flex h-14 items-start gap-2 bg-gradient-to-b from-black/75 via-black/35 to-transparent px-3 pt-2.5 transition-opacity duration-300"
              :class="chrome ? 'opacity-100' : 'pointer-events-none opacity-0'"
              @click.self="onStageClick" @dblclick.self="onDblClick"
            >
              <FileVideo class="mt-2 h-4 w-4 shrink-0 text-white/80" :stroke-width="1.75" />
              <h2 :id="titleId" class="mt-1.5 min-w-0 truncate text-sm font-medium leading-6 text-white/95" :title="entry?.name">{{ entry?.name }}</h2>
              <span
                v-if="fixes.includes('ctts') && !mini" class="mt-2 shrink-0 cursor-help rounded-full bg-white/15 px-2 py-0.5 text-[11px] leading-4 text-white/85"
                :title="fixTip" tabindex="0" :aria-label="fixTip"
              >{{ $t('player.fixedBadge') }}</span>
              <button
                v-if="fixes.includes('ctts') && !mini"
                class="mt-1.5 flex h-6 shrink-0 items-center gap-1 rounded-full bg-white/15 px-2.5 text-[11px] text-white/90 transition-colors hover:bg-white/25 disabled:cursor-default disabled:opacity-50"
                :disabled="!!(entry && saveState[entry.path])" :title="$t('player.saveFixedTip', { name: fixedName })" @click="saveFixed"
              ><Check v-if="entry && saveState[entry.path] === 'saved'" class="h-3.5 w-3.5" /><Save v-else class="h-3.5 w-3.5" />{{ saveLabel }}</button>
              <span class="flex-1" />
              <template v-if="!mini">
                <button :class="icon" :title="$t('common.download')" :aria-label="$t('common.download')" @click="entry && emit('download', entry)"><Download class="h-[18px] w-[18px]" /></button>
                <button v-if="!fullscreen" :class="icon" :title="$t('player.minimize')" :aria-label="$t('player.minimize')" @click="mini = true"><Minus class="h-[18px] w-[18px]" /></button>
              </template>
              <button v-else :class="icon" :title="$t('player.expand')" :aria-label="$t('player.expand')" @click="mini = false"><Maximize2 class="h-4 w-4" /></button>
              <button v-if="!fullscreen && !mini" :class="icon" :title="maximized ? $t('player.restore') : $t('player.maximize')" :aria-label="maximized ? $t('player.restore') : $t('player.maximize')" @click="maximized = !maximized">
                <Copy v-if="maximized" class="h-4 w-4 -scale-x-100" /><Square v-else class="h-4 w-4" />
              </button>
              <button :class="icon" :title="$t('common.close')" :aria-label="$t('common.close')" @click="close"><X class="h-5 w-5" /></button>
            </div>

            <!-- Control bar -->
            <div
              class="absolute inset-x-0 bottom-0 bg-gradient-to-t from-black/80 via-black/40 to-transparent px-3 pb-2 pt-10 transition-opacity duration-300"
              :class="chrome ? 'opacity-100' : 'pointer-events-none opacity-0'"
              @click.self="onStageClick" @dblclick.self="onDblClick"
            >
              <!-- Progress: buffered, played, hover preview time; drag to scrub. -->
              <div
                ref="bar" class="group/bar relative mx-1 flex h-4 cursor-pointer items-center touch-none"
                role="slider" :aria-label="$t('player.progress')" :aria-valuemin="0" :aria-valuemax="Math.round(duration)" :aria-valuenow="Math.round(shown)" :aria-valuetext="fmt(shown)"
                @pointerdown="barDown" @pointermove="barMove" @pointerup="barUp" @pointercancel="barUp" @pointerleave="hoverX = null"
              >
                <div class="relative h-1 w-full overflow-hidden rounded-full bg-white/25 transition-[height] group-hover/bar:h-1.5" :class="dragging && '!h-1.5'">
                  <div class="absolute inset-y-0 left-0 bg-white/35" :style="{ width: pct(buffered) }" />
                  <div class="absolute inset-y-0 left-0 bg-brand-500" :style="{ width: pct(shown) }" />
                </div>
                <div
                  class="absolute h-3.5 w-3.5 -translate-x-1/2 rounded-full bg-white shadow transition-transform group-hover/bar:scale-100"
                  :class="dragging ? 'scale-100' : 'scale-0'" :style="{ left: pct(shown) }"
                />
                <div
                  v-if="(hoverX !== null || dragging) && duration"
                  class="pointer-events-none absolute bottom-5 -translate-x-1/2 rounded bg-black/80 px-1.5 py-0.5 text-xs tabular-nums"
                  :style="{ left: dragging ? pct(scrub) : `${(hoverX ?? 0) * 100}%` }"
                >{{ fmt(dragging ? scrub : (hoverX ?? 0) * duration) }}</div>
              </div>

              <div class="mt-1 flex items-center gap-1">
                <button :class="icon" :title="paused ? $t('player.play') : $t('player.pause')" :aria-label="paused ? $t('player.play') : $t('player.pause')" @click="togglePlay">
                  <Play v-if="paused" class="h-5 w-5" fill="currentColor" /><Pause v-else class="h-5 w-5" fill="currentColor" />
                </button>
                <div class="group/vol flex items-center">
                  <button :class="icon" :title="muted ? $t('player.unmute') : $t('player.mute')" :aria-label="muted ? $t('player.unmute') : $t('player.mute')" @click="toggleMute">
                    <component :is="volIcon" class="h-5 w-5" />
                  </button>
                  <input
                    v-if="!mini" type="range" min="0" max="1" step="0.01" :value="muted ? 0 : volume" :aria-label="$t('player.volume')"
                    class="h-1 w-0 cursor-pointer accent-white opacity-0 transition-all duration-200 group-hover/vol:mr-2 group-hover/vol:w-20 group-hover/vol:opacity-100 focus-visible:mr-2 focus-visible:w-20 focus-visible:opacity-100"
                    @input="setVolume(Number(($event.target as HTMLInputElement).value))"
                  />
                </div>
                <span class="ml-1 text-[13px] tabular-nums text-white/90">{{ fmt(shown) }} <span class="text-white/50">/ {{ fmt(duration) }}</span></span>

                <div class="ml-auto flex items-center gap-1">
                  <!-- Speed, loop and shortcuts sit directly on the bar; their lists open upwards. -->
                  <template v-if="!mini">
                  <div class="relative">
                    <button
                      class="flex h-9 min-w-12 items-center justify-center rounded-md px-2 text-[13px] font-medium tabular-nums text-white/90 transition-colors hover:bg-white/15 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/60"
                      :class="[menu === 'speed' && 'bg-white/15', rate !== 1 && 'text-brand-300']"
                      :title="$t('player.speed')" :aria-label="$t('player.speed')" :aria-expanded="menu === 'speed'" @click.stop="menu = menu === 'speed' ? '' : 'speed'"
                    >{{ rate === 1 ? $t('player.speedShort') : rate + '×' }}</button>
                    <div v-if="menu === 'speed'" class="absolute bottom-11 z-10 overflow-hidden rounded-xl bg-neutral-900/90 py-1.5 text-[13px] shadow-xl ring-1 ring-white/10 backdrop-blur-md left-1/2 w-28 -translate-x-1/2" role="menu" @click.stop>
                      <button
                        v-for="r in [...RATES].reverse()" :key="r" role="menuitemradio" :aria-checked="r === rate"
                        class="flex w-full items-center justify-center px-3 py-1.5 tabular-nums hover:bg-white/10" :class="r === rate && 'font-semibold text-brand-300'"
                        @click="setRate(r); menu = ''"
                      >{{ r === 1 ? $t('player.normal') : r + '×' }}</button>
                    </div>
                  </div>
                  <button
                    :class="[icon, loop && '!text-brand-300 bg-white/10']" role="switch" :aria-checked="loop"
                    :title="loop ? $t('player.loopOn') : $t('player.loop')" :aria-label="$t('player.loop')"
                    @click="loop = !loop; flash(loop ? $t('player.loopOn') : $t('player.loopOff'))"
                  ><Repeat class="h-5 w-5" /></button>
                  </template>
                  <button v-if="castAvailable && !mini" :class="icon" :title="$t('player.cast')" :aria-label="$t('player.cast')" @click="cast">
                    <Airplay v-if="castKind === 'airplay'" class="h-5 w-5" /><Cast v-else class="h-5 w-5" />
                  </button>
                  <div v-if="!mini" class="relative hidden sm:block">
                    <button
                      :class="[icon, menu === 'keys' && 'bg-white/15']" :title="$t('player.shortcutsTitle')" :aria-label="$t('player.shortcutsTitle')"
                      :aria-expanded="menu === 'keys'" @click.stop="menu = menu === 'keys' ? '' : 'keys'"
                    ><Keyboard class="h-5 w-5" /></button>
                    <div v-if="menu === 'keys'" class="absolute bottom-11 z-10 overflow-hidden rounded-xl bg-neutral-900/90 py-1.5 text-[13px] shadow-xl ring-1 ring-white/10 backdrop-blur-md right-0 w-64 px-1" @click.stop>
                      <p class="px-2.5 pb-1.5 pt-1 text-xs font-medium text-white/60">{{ $t('player.shortcutsTitle') }}</p>
                      <div v-for="[k, label] in SHORTCUTS" :key="k" class="flex items-center gap-3 px-2.5 py-1">
                        <kbd class="min-w-14 rounded bg-white/10 px-1.5 py-0.5 text-center font-sans text-[11px] text-white/90">{{ k }}</kbd>
                        <span class="text-white/80">{{ $t(label) }}</span>
                      </div>
                    </div>
                  </div>
                  <button v-if="pipSupported" :class="icon" :title="$t('player.pip')" :aria-label="$t('player.pip')" @click="pip"><PictureInPicture2 class="h-5 w-5" /></button>
                  <button :class="icon" :title="fullscreen ? $t('player.exitFullscreen') : $t('player.fullscreen')" :aria-label="fullscreen ? $t('player.exitFullscreen') : $t('player.fullscreen')" @click="toggleFullscreen">
                    <Minimize v-if="fullscreen" class="h-5 w-5" /><Maximize v-else class="h-5 w-5" />
                  </button>
                </div>
              </div>
            </div>
          </div>
      </div>
    </template>
  </Teleport>
</template>

<style>
.vp-overlay { animation: vp-fade-in 0.18s ease-out; }
.vp-content { animation: vp-zoom-in 0.2s cubic-bezier(0.16, 1, 0.3, 1); }
@keyframes vp-fade-in { from { opacity: 0; } }
@keyframes vp-zoom-in { from { opacity: 0; scale: 0.97; } }
@media (prefers-reduced-motion: reduce) {
  .vp-overlay, .vp-content { animation: none; }
}
</style>
