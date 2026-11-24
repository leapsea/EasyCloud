<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue'
import { DialogContent, DialogDescription, DialogOverlay, DialogPortal, DialogRoot, DialogTitle } from 'reka-ui'
import { useModalLayer } from './modalStack'

// large: for editors that need room (two columns, a tree); taller as well.
const props = defineProps<{ title: string; description?: string; wide?: boolean; large?: boolean }>()
const open = defineModel<boolean>('open', { required: true })

const layer = useModalLayer()
watch(open, (o) => (o ? layer.push() : layer.pop()), { immediate: true })
onBeforeUnmount(layer.pop)

// Each nested level sits higher on the z-axis and further down the page; a
// dialog covered by a child scales back slightly behind the child's overlay.
const level = computed(() => Math.max(0, layer.depth.value))
const overlayStyle = computed(() => ({ zIndex: 40 + level.value * 10 }))
const contentStyle = computed(() => ({
  zIndex: 41 + level.value * 10,
  top: `${(props.large ? 6 : 14) + level.value * 6}%`,
  scale: layer.behind.value > 0 ? String(1 - 0.04 * layer.behind.value) : '1',
}))
</script>

<template>
  <DialogRoot v-model:open="open">
    <DialogPortal>
      <Transition name="fade">
        <DialogOverlay class="fixed inset-0 bg-slate-900/40 backdrop-blur-[2px]" :class="level > 0 && 'bg-slate-900/30 backdrop-blur-[1px]'" :style="overlayStyle" />
      </Transition>
      <Transition name="fade">
        <DialogContent
          class="fixed left-1/2 flex -translate-x-1/2 flex-col rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-6 shadow-[0_24px_64px_-16px_rgb(15_23_42/0.35)] outline-none transition-[scale,top] duration-200 ease-out"
          :class="[large ? 'max-h-[88vh] w-[min(94vw,920px)]' : wide ? 'max-h-[76vh] w-[min(92vw,540px)]' : 'max-h-[76vh] w-[min(92vw,420px)]', level > 0 && 'shadow-[0_32px_80px_-12px_rgb(15_23_42/0.5)]']"
          :style="contentStyle"
        >
          <!-- Title and actions stay put; only the body scrolls when content is tall. -->
          <div class="shrink-0">
            <DialogTitle class="text-[15px] font-semibold leading-6">{{ title }}</DialogTitle>
            <DialogDescription v-if="description" class="muted mt-1 text-[13px] leading-5">{{ description }}</DialogDescription>
          </div>
          <div class="-mx-1 mt-5 min-h-0 flex-1 overflow-y-auto px-1 pb-1"><slot /></div>
          <div class="mt-5 flex shrink-0 flex-wrap justify-end gap-2"><slot name="footer" /></div>
        </DialogContent>
      </Transition>
    </DialogPortal>
  </DialogRoot>
</template>
