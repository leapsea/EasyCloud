<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import * as echarts from 'echarts/core'
import { LineChart, PieChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsCoreOption } from 'echarts/core'
import { useTheme } from '@/theme/theme'
import { locale } from '@/i18n'

echarts.use([LineChart, PieChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

// Incremental updates merge into the existing chart (series matched by id), so
// new points animate in instead of the whole chart redrawing. Changing
// `resetKey` (e.g. the time range) replaces everything with a fresh entrance animation.
const props = defineProps<{ option: EChartsCoreOption; height?: string; resetKey?: string | number }>()
const el = ref<HTMLDivElement>()
const chart = shallowRef<echarts.ECharts>()
const theme = useTheme()

function init() {
  chart.value?.dispose()
  chart.value = echarts.init(el.value!, theme.dark.value ? 'dark' : undefined, { renderer: 'canvas' })
  chart.value.setOption({ backgroundColor: 'transparent', ...props.option })
}
const ro = new ResizeObserver(() => chart.value?.resize())
onMounted(() => { init(); ro.observe(el.value!) })
onBeforeUnmount(() => { ro.disconnect(); chart.value?.dispose() })
watch(theme.dark, init) // re-create with the matching ECharts theme
watch(() => theme.state.accent, init)
watch(locale, init) // series names are translated, so rebuild instead of merging
watch(() => props.option, (o) => chart.value?.setOption(o, { lazyUpdate: true }))
watch(() => props.resetKey, () => chart.value?.setOption({ backgroundColor: 'transparent', ...props.option }, { notMerge: true }))
</script>

<template>
  <div ref="el" :style="{ height: height ?? '220px' }" class="w-full" />
</template>
