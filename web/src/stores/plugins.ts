import { defineStore } from 'pinia'
import { ref } from 'vue'
import { pluginsApi, type ViewerPlugin } from '@/api/plugins'

// MIME registry of viewer plugins; built-in viewers are the fallback.
export const usePlugins = defineStore('plugins', () => {
  const viewers = ref<ViewerPlugin[]>([])
  const office = ref(false)
  let loaded: Promise<void> | null = null

  function load(force = false) {
    if (!loaded || force) {
      loaded = pluginsApi.viewers().then((r) => { viewers.value = r.items; office.value = r.office }).catch(() => {})
    }
    return loaded
  }

  function viewerFor(name: string, mime?: string): ViewerPlugin | undefined {
    const ext = name.includes('.') ? name.slice(name.lastIndexOf('.')).toLowerCase() : ''
    const m = (mime ?? '').split(';')[0].trim().toLowerCase()
    return viewers.value.find((v) =>
      v.extensions.some((e) => e.toLowerCase() === ext) ||
      v.mimes.some((p) => (p.endsWith('/*') ? m.startsWith(p.slice(0, -1)) : p === m)))
  }

  return { viewers, office, load, viewerFor }
})

export const officeExtensions = new Set(['doc', 'docx', 'odt', 'rtf', 'docm', 'dotx', 'xls', 'xlsx', 'ods', 'csv', 'xlsm', 'ppt', 'pptx', 'odp', 'ppsx'])
// PDFs open in the browser's own viewer, not the office editor.
export const isOffice = (name: string) => officeExtensions.has(name.split('.').pop()?.toLowerCase() ?? '')
