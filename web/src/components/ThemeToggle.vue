<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { computed } from 'vue'
import { Monitor, Moon, Sun } from 'lucide-vue-next'
import { useTheme } from '@/theme/theme'
const { t } = useI18n()

// Compact light → dark → system switch for headers and the sidebar.
const theme = useTheme()
const meta = computed(() => ({
  light: { icon: Sun, label: t('theme.toggle.light') },
  dark: { icon: Moon, label: t('theme.toggle.dark') },
  system: { icon: Monitor, label: t('theme.toggle.system') },
}[theme.state.mode]))
</script>

<template>
  <button class="btn btn-sm !px-1.5" :title="meta.label" :aria-label="meta.label" @click="theme.cycle()">
    <component :is="meta.icon" class="h-3.5 w-3.5" />
  </button>
</template>
