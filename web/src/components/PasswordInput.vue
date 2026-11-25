<script setup lang="ts">
import { computed, ref, useAttrs } from 'vue'
import { Eye, EyeOff } from 'lucide-vue-next'

// A password field with a hold-to-reveal button: the text shows only while
// the button is held down (mouse, touch or Space/Enter) and is masked again
// on release, so a password is never left visible on screen.
defineOptions({ inheritAttrs: false })
const model = defineModel<string>({ required: true })
const shown = ref(false)
// class/style lay out the whole field (margins, width, font); every other
// attribute (placeholder, autocomplete, autofocus…) belongs to the input.
const attrs = useAttrs()
const inputAttrs = computed(() => { const { class: _c, style: _s, ...rest } = attrs; return rest })

function press(e: PointerEvent) {
  if (e.button !== 0) return
  shown.value = true
  // Releasing outside the button still counts as releasing it.
  try { (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId) } catch { /* pointer already gone */ }
}
const hide = () => (shown.value = false)
</script>

<template>
  <div class="relative" :class="$attrs.class" :style="$attrs.style as any">
    <input v-model="model" v-bind="inputAttrs":type="shown ? 'text' : 'password'" class="input w-full pr-9" />
    <button
      type="button"
      class="absolute inset-y-0 right-0 flex w-9 items-center justify-center text-[var(--text-muted)] hover:text-[var(--text)]"
      :title="$t('common.holdToShowPw')"
      :aria-label="$t('common.holdToShowPw')"
      :aria-pressed="shown"
      @mousedown.prevent
      @pointerdown="press"
      @pointerup="hide"
      @pointercancel="hide"
      @lostpointercapture="hide"
      @keydown.space.prevent="shown = true"
      @keydown.enter.prevent="shown = true"
      @keyup.space="hide"
      @keyup.enter="hide"
      @blur="hide"
      @contextmenu.prevent
    >
      <EyeOff v-if="shown" class="h-4 w-4" />
      <Eye v-else class="h-4 w-4" />
    </button>
  </div>
</template>
