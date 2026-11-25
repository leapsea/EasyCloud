<script setup lang="ts">
import { ArrowLeft } from 'lucide-vue-next'
import { useRoute, useRouter } from 'vue-router'
import OfficeEditor from './OfficeEditor.vue'

// Standalone editor page (/office?path=…), for direct links. Inside the app
// documents open in OfficeOverlay instead, without leaving the page.
const route = useRoute()
const router = useRouter()
const path = String(route.query.path ?? '')
// Back where the user came from; a page opened on its own goes to the files.
function back() {
  if (window.history.length > 1) router.back()
  else router.push('/files')
}
</script>

<template>
  <div class="flex h-screen flex-col">
    <header class="flex items-center gap-2 border-b border-[var(--border)] bg-[var(--surface)] px-3 py-2 text-sm">
      <button class="btn btn-sm" @click="back"><ArrowLeft class="h-3.5 w-3.5" />{{ $t('common.back') }}</button>
      <span class="truncate font-medium">{{ path.split('/').pop() }}</span>
    </header>
    <OfficeEditor :path="path" class="min-h-0 flex-1" />
  </div>
</template>
