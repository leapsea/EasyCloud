import { computed, ref } from 'vue'
import { useUploads } from '@/stores/upload'
import { useTasks } from '@/stores/tasks'

// Whether the transfer panel is shown. Separate from whether there is anything
// in it: the panel opens itself when work starts, hides itself once all of it
// has finished cleanly, and the sidebar button toggles it at any time.
export const transferOpen = ref(false)

// Counts shared by the panel and the sidebar button.
export function useTransferCounts() {
  const up = useUploads()
  const tasks = useTasks()
  const busy = computed(() => up.active.length + tasks.visible.filter((t) => t.status === 'queued' || t.status === 'running').length)
  const failed = computed(() => up.items.filter((i) => i.state === 'error').length + tasks.visible.filter((t) => t.status === 'failed').length)
  const total = computed(() => up.items.length + tasks.visible.length)
  return { busy, failed, total }
}
