<script setup lang="ts">
import { computed } from 'vue'
import { Languages } from 'lucide-vue-next'
import { useLocalePref } from '@/i18n'

// One-click switch between the two supported languages (for pages without the account settings).
const { locale, setPref } = useLocalePref()
const next = computed(() => (locale.value === 'zh-CN' ? 'en' : 'zh-CN'))
const label = computed(() => (locale.value === 'zh-CN' ? 'Switch to English' : '切换到简体中文'))
</script>

<template>
  <button class="btn btn-sm" :title="label" :aria-label="label" @click="setPref(next)">
    <Languages class="h-3.5 w-3.5" />{{ locale === 'zh-CN' ? 'EN' : '中' }}
  </button>
</template>
