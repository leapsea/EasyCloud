<script setup lang="ts">
import { inject } from 'vue'
import { Menu } from 'lucide-vue-next'

// The one title bar every page uses: fixed height, vertically centred.
//   title    — page name (or use the #title slot, e.g. for a breadcrumb)
//   subtitle — muted one-liner, truncated to fit
//   default slot — inline extras right after the title (tabs, badges)
//   #actions — right-aligned controls
defineProps<{ title?: string; subtitle?: string }>()
const openNav = inject<() => void>('openNav', () => {})
</script>

<template>
  <header class="mb-2 flex h-11 min-w-0 shrink-0 items-center gap-3">
    <button class="btn btn-sm !px-1.5 md:hidden" :aria-label="$t('nav.menu')" @click="openNav"><Menu class="h-4 w-4" /></button>
    <div class="flex min-w-0 items-center gap-2.5">
      <slot name="title"><h1 class="shrink-0 truncate text-base font-semibold leading-6">{{ title }}</h1></slot>
      <span v-if="subtitle" class="muted hidden truncate text-[13px] sm:inline">{{ subtitle }}</span>
      <slot />
    </div>
    <div v-if="$slots.actions" class="ml-auto flex shrink-0 items-center gap-2"><slot name="actions" /></div>
  </header>
</template>
