import { ref, watch } from 'vue'

// Waterfall view: images per row. 0 = fit to width (~200px tiles).
// A per-browser preference, like theme and language.
const KEY = 'ec.gallery.cols'
export const GALLERY_COL_CHOICES = [0, 2, 3, 4, 5, 6, 8] as const

function read(): number {
  try {
    const n = Number(localStorage.getItem(KEY))
    return (GALLERY_COL_CHOICES as readonly number[]).includes(n) ? n : 0
  } catch { return 0 }
}

export const galleryCols = ref(read())
watch(galleryCols, (n) => { try { localStorage.setItem(KEY, String(n)) } catch { /* private mode */ } })
