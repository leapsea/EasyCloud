<script setup lang="ts">
import PageHeader from '@/components/PageHeader.vue'
import { useI18n } from 'vue-i18n'
import { tint, useTheme } from '@/theme/theme'
import { computed, onBeforeUnmount, onMounted, ref, shallowRef } from 'vue'
import { adminApi, type MetricPoint, type SystemInfo } from '@/api/admin'
import { realtime } from '@/api/realtime'
import { useToast } from '@/stores/toast'
import Chart from '@/components/Chart.vue'
import { formatSize } from '@/components/format'
const { t } = useI18n()

const toast = useToast()
const theme = useTheme()
const accentLine = computed(() => (theme.dark.value ? tint(theme.state.accent, 0.3) : theme.state.accent))
const info = ref<SystemInfo | null>(null)
const range = ref('1h')
const ranges = computed(() => [
  { v: '1h', l: t('dashboard.hours', { n: 1 }) }, { v: '6h', l: t('dashboard.hours', { n: 6 }) }, { v: '24h', l: t('dashboard.hours', { n: 24 }) },
  { v: '168h', l: t('dashboard.days', { n: 7 }) }, { v: '720h', l: t('dashboard.days', { n: 30 }) },
])
const metricNames = ['cpu', 'mem', 'disk_read', 'disk_write', 'net_rx', 'net_tx']
const series = shallowRef<Record<string, [number, number][]>>({})
const live = ref<Record<string, number>>({})
const historyVersion = ref(0)
// The time axis always spans the whole selected range, so a fresh install shows
// its first samples at the right edge instead of a squashed axis.
const win = ref({ from: Date.now() - 3600_000, to: Date.now() })

async function loadHistory() {
  try {
    const r = await adminApi.metrics(metricNames, range.value)
    const s: Record<string, [number, number][]> = {}
    for (const [k, pts] of Object.entries(r.series)) s[k] = (pts as MetricPoint[]).map((p) => [p.t * 1000, p.v])
    series.value = s
    win.value = { from: r.from * 1000, to: r.to * 1000 }
    historyVersion.value++ // full redraw only for a freshly loaded history
  } catch (e) { toast.error(e) }
}
async function loadInfo() { try { info.value = await adminApi.system() } catch (e) { toast.error(e) } }

let unsub: (() => void) | undefined
let infoTimer: number | undefined
onMounted(() => {
  loadInfo(); loadHistory()
  infoTimer = window.setInterval(loadInfo, 30_000)
  unsub = realtime.subscribe('metrics', (d) => {
    const m = d as Record<string, number>
    live.value = m
    if (range.value !== '1h') return
    const t = m.t * 1000, cut = t - 3600_000
    win.value = { from: cut, to: t }
    const s = { ...series.value }
    for (const k of metricNames) {
      if (m[k] === undefined) continue
      s[k] = [...(s[k] ?? []).filter((p) => p[0] >= cut), [t, m[k]]]
    }
    series.value = s
  }, loadHistory)
})
onBeforeUnmount(() => { unsub?.(); clearInterval(infoTimer) })

function setRange(v: string) { range.value = v; loadHistory() }

const axisTime = computed(() => ({
  type: 'time', axisLine: { show: false }, splitLine: { show: false }, splitNumber: 4,
  min: win.value.from, max: win.value.to,
  axisLabel: { hideOverlap: true, formatter: ['1h', '6h', '24h'].includes(range.value) ? '{HH}:{mm}' : '{MM}-{dd}' },
}))
// Gap markers are null: show a dash instead of crashing the formatter.
const fmt = (f: (v: number) => string) => (v: unknown) => (typeof v === 'number' ? f(v) : '—')
const tooltip = { trigger: 'axis', valueFormatter: fmt(String) }
const grid = { left: 50, right: 16, top: 30, bottom: 24 }
// Live samples arrive every 5 s: a linear update animation makes the line glide forward.
const anim = { animationDuration: 800, animationDurationUpdate: 700, animationEasingUpdate: 'linear' as const }
// Missing samples (service stopped, host asleep) must show as a gap, not a straight
// line bridging the hole: wherever two samples are much further apart than the usual
// step, insert a null point, which ECharts renders as a break (connectNulls is off).
function withGaps(data: [number, number][]): [number, number | null][] {
  if (data.length < 3) return data
  const steps = data.slice(1).map((p, i) => p[0] - data[i][0]).sort((a, b) => a - b)
  const step = steps[steps.length >> 1] // median: robust against the gaps themselves
  const limit = Math.max(step * 3, 15_000)
  const out: [number, number | null][] = [data[0]]
  for (let i = 1; i < data.length; i++) {
    if (data[i][0] - data[i - 1][0] > limit) out.push([data[i - 1][0] + step, null])
    out.push(data[i])
  }
  return out
}
function line(name: string, data: [number, number][], color: string, area = false) {
  // No client-side sampling: the server already downsamples longer ranges, and LTTB would drop the gap markers.
  return { id: name, name, type: 'line', showSymbol: false, smooth: false, connectNulls: false, data: withGaps(data), lineStyle: { width: 1.5, color }, itemStyle: { color }, areaStyle: area ? { opacity: 0.12, color } : undefined }
}
const bytesAxis = { type: 'value', axisLabel: { formatter: (v: number) => formatSize(v) }, splitLine: { lineStyle: { opacity: 0.3 } } }
const rateAxis = { type: 'value', axisLabel: { formatter: (v: number) => formatSize(v) + '/s' }, splitLine: { lineStyle: { opacity: 0.3 } } }

const cpuOpt = computed(() => ({
  ...anim, grid, tooltip: { ...tooltip, valueFormatter: fmt((v) => `${v.toFixed(1)}%`) }, xAxis: axisTime.value,
  yAxis: { type: 'value', max: 100, axisLabel: { formatter: '{value}%' }, splitLine: { lineStyle: { opacity: 0.3 } } },
  series: [line('CPU', series.value.cpu ?? [], accentLine.value, true)],
}))
const memOpt = computed(() => ({
  ...anim, grid, tooltip: { ...tooltip, valueFormatter: fmt((v) => formatSize(v)) }, xAxis: axisTime.value,
  yAxis: { ...bytesAxis, max: info.value?.host.memTotal || undefined },
  series: [line(t('dashboard.memory'), series.value.mem ?? [], '#8b5cf6', true)],
}))
const diskOpt = computed(() => ({
  ...anim, grid, legend: { top: 0, right: 0 }, tooltip: { ...tooltip, valueFormatter: fmt((v) => formatSize(v) + '/s') }, xAxis: axisTime.value, yAxis: rateAxis,
  series: [line(t('dashboard.read'), series.value.disk_read ?? [], '#10b981'), line(t('dashboard.write'), series.value.disk_write ?? [], '#f59e0b')],
}))
const netOpt = computed(() => ({
  ...anim, grid, legend: { top: 0, right: 0 }, tooltip: { ...tooltip, valueFormatter: fmt((v) => formatSize(v) + '/s') }, xAxis: axisTime.value, yAxis: rateAxis,
  series: [line(t('dashboard.down'), series.value.net_rx ?? [], '#06b6d4'), line(t('dashboard.up'), series.value.net_tx ?? [], '#ec4899')],
}))
const storageOpt = computed(() => {
  const top = info.value?.storage.topLevel ?? {}
  const data = Object.entries(top).filter(([, v]) => v > 0).sort((a, b) => b[1] - a[1]).map(([name, value]) => ({ name, value }))
  return {
    tooltip: { trigger: 'item', valueFormatter: (v: number) => formatSize(v) },
    series: [{ type: 'pie', radius: ['45%', '72%'], itemStyle: { borderRadius: 6, borderWidth: 2, borderColor: 'transparent' }, label: { formatter: '{b}\n{d}%' }, data }],
  }
})
const diskPct = computed(() => info.value ? Math.round((info.value.host.diskUsed / info.value.host.diskTotal) * 100) : 0)
function uptime(s: number) { const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600); return d ? t('dashboard.uptimeDH', { d, h }) : t('dashboard.hours', { n: h }) }
</script>

<template>
  <section class="flex flex-col gap-4 pb-6">
    <PageHeader :title="$t('nav.dashboard')" :subtitle="info ? `${info.host.hostname} · ${info.host.platform} · ${info.host.arch} · ${$t('dashboard.uptime', { t: uptime(info.host.uptimeSec) })}` : ''" class="!-mb-2" />

    <div v-if="info" class="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <div class="card p-4">
        <p class="muted text-xs">{{ $t('dashboard.cpu', { n: info.host.cpus }) }}</p>
        <p class="mt-1 text-2xl font-semibold tabular-nums">{{ live.cpu?.toFixed(1) ?? '—' }}<span class="text-sm">%</span></p>
        <p class="muted text-xs">{{ $t('dashboard.load', { v: (live.load1 ?? info.host.load1).toFixed(2) }) }}</p>
      </div>
      <div class="card p-4">
        <p class="muted text-xs">{{ $t('dashboard.memory') }}</p>
        <p class="mt-1 text-2xl font-semibold tabular-nums">{{ live.mem ? formatSize(live.mem) : '—' }}</p>
        <p class="muted text-xs">{{ $t('dashboard.total', { v: formatSize(info.host.memTotal) }) }}</p>
      </div>
      <div class="card p-4">
        <p class="muted text-xs">{{ $t('dashboard.disk', { fs: info.host.diskFs }) }}</p>
        <p class="mt-1 text-2xl font-semibold tabular-nums">{{ diskPct }}<span class="text-sm">%</span></p>
        <div class="mt-1 h-1.5 overflow-hidden rounded bg-[var(--border)]">
          <div class="h-full transition-[width]" :class="diskPct > 90 ? 'bg-red-500' : 'bg-brand-500'" :style="{ width: diskPct + '%' }" />
        </div>
        <p class="muted mt-1 text-xs">{{ $t('dashboard.free', { free: formatSize(info.host.diskFree), total: formatSize(info.host.diskTotal) }) }}</p>
      </div>
      <div class="card p-4">
        <p class="muted text-xs">{{ $t('dashboard.index') }}</p>
        <p class="mt-1 text-2xl font-semibold tabular-nums">{{ info.storage.indexedFiles.toLocaleString() }}</p>
        <p class="muted text-xs">{{ formatSize(info.storage.indexedBytes) }} · {{ $t('dashboard.users', { n: info.users }) }}</p>
      </div>
    </div>

    <div class="flex flex-wrap gap-1">
      <button v-for="r in ranges" :key="r.v" class="btn btn-sm" :class="{ 'btn-primary': range === r.v }" @click="setRange(r.v)">{{ r.l }}</button>
    </div>
    <div class="grid gap-3 lg:grid-cols-2">
      <div class="card p-3"><p class="text-sm font-medium">CPU</p><Chart :option="cpuOpt" :reset-key="historyVersion" /></div>
      <div class="card p-3"><p class="text-sm font-medium">{{ $t('dashboard.memory') }}</p><Chart :option="memOpt" :reset-key="historyVersion" /></div>
      <div class="card p-3"><p class="text-sm font-medium">{{ $t('dashboard.diskIo') }}</p><Chart :option="diskOpt" :reset-key="historyVersion" /></div>
      <div class="card p-3"><p class="text-sm font-medium">{{ $t('dashboard.network') }}</p><Chart :option="netOpt" :reset-key="historyVersion" /></div>
    </div>
    <div class="grid gap-3 lg:grid-cols-2">
      <div class="card p-3"><p class="text-sm font-medium">{{ $t('dashboard.storage') }}</p><Chart :option="storageOpt" height="260px" /></div>
      <div v-if="info" class="card p-4 text-sm">
        <p class="font-medium">{{ $t('dashboard.system') }}</p>
        <dl class="mt-3 grid grid-cols-[minmax(7rem,max-content)_1fr] gap-x-4 gap-y-2">
          <dt class="muted">{{ $t('dashboard.version') }}</dt><dd>{{ info.app.version }}</dd>
          <dt class="muted">{{ $t('dashboard.database') }}</dt><dd class="break-all font-mono text-xs">{{ info.app.database }}</dd>
          <dt class="muted">{{ $t('dashboard.dataRoot') }}</dt><dd class="break-all font-mono text-xs">{{ info.app.dataRoot }}</dd>
          <dt class="muted">{{ $t('dashboard.os') }}</dt><dd>{{ info.host.os }} / {{ info.host.arch }}</dd>
        </dl>
      </div>
    </div>
  </section>
</template>
