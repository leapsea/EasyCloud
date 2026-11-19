<script setup lang="ts">
import { computed } from 'vue'
import {
  File, FileArchive, FileAudio, FileCode, FileImage, FileSpreadsheet, FileText, FileVideo, Folder, Presentation,
} from 'lucide-vue-next'

// One consistent, colour-coded icon per file kind.
const props = withDefaults(defineProps<{ entry: { isDir: boolean; mime?: string; name: string }; size?: number }>(), { size: 18 })

const kind = computed(() => {
  const e = props.entry
  if (e.isDir) return { icon: Folder, color: 'text-amber-500', fill: '#fcd34d' }
  const m = e.mime ?? ''
  const ext = e.name.split('.').pop()?.toLowerCase() ?? ''
  if (m.startsWith('image/') || ['heic', 'heif', 'raw', 'cr2', 'nef', 'arw', 'dng'].includes(ext)) return { icon: FileImage, color: 'text-emerald-500' }
  if (m.startsWith('video/') || ['mkv'].includes(ext)) return { icon: FileVideo, color: 'text-violet-500' }
  if (m.startsWith('audio/')) return { icon: FileAudio, color: 'text-pink-500' }
  if (m.includes('pdf')) return { icon: FileText, color: 'text-red-500' }
  if (['zip', 'rar', '7z', 'tar', 'gz', 'tgz', 'xz', 'bz2', 'zst'].includes(ext)) return { icon: FileArchive, color: 'text-orange-500' }
  if (['xls', 'xlsx', 'csv', 'ods'].includes(ext)) return { icon: FileSpreadsheet, color: 'text-green-600' }
  if (['ppt', 'pptx', 'odp', 'key'].includes(ext)) return { icon: Presentation, color: 'text-orange-600' }
  if (['doc', 'docx', 'odt', 'rtf', 'pages'].includes(ext)) return { icon: FileText, color: 'text-blue-600' }
  if (['js', 'ts', 'go', 'py', 'vue', 'json', 'yaml', 'yml', 'sh', 'html', 'css', 'rs', 'java', 'c', 'cpp', 'h', 'sql', 'toml'].includes(ext)) return { icon: FileCode, color: 'text-sky-500' }
  if (m.startsWith('text/') || ['md', 'txt', 'log'].includes(ext)) return { icon: FileText, color: 'text-slate-500' }
  return { icon: File, color: 'text-slate-400' }
})
</script>

<template>
  <component :is="kind.icon" :size="size" :stroke-width="1.75" :class="['shrink-0', kind.color]" :fill="kind.fill ?? 'none'" aria-hidden="true" />
</template>
