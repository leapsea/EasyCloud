import { computed, reactive } from 'vue'

// Open dialogs in opening order. Nested dialogs get a higher layer and an
// offset, and dialogs underneath recede, so the hierarchy stays visible.
const stack = reactive<number[]>([])
let seq = 0

export function useModalLayer() {
  const id = ++seq
  const depth = computed(() => stack.indexOf(id))
  const isTop = computed(() => stack.length > 0 && stack[stack.length - 1] === id)
  const behind = computed(() => (depth.value < 0 ? 0 : stack.length - 1 - depth.value))
  return {
    depth, isTop, behind,
    push: () => { if (!stack.includes(id)) stack.push(id) },
    pop: () => { const i = stack.indexOf(id); if (i >= 0) stack.splice(i, 1) },
  }
}
