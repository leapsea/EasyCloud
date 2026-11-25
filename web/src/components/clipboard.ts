// Copies text to the clipboard. navigator.clipboard exists only in secure
// contexts (HTTPS or localhost); on a plain-HTTP LAN address such as
// http://192.168.1.10:8090 it is missing, so fall back to the legacy
// execCommand('copy') on a temporary, off-screen textarea.
export async function copyText(text: string): Promise<void> {
  if (navigator.clipboard && window.isSecureContext) {
    try { await navigator.clipboard.writeText(text); return } catch { /* fall through */ }
  }
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  ta.style.cssText = 'position:fixed;top:0;left:-9999px;opacity:0'
  // Inside an open dialog, focus is trapped there: attach the textarea to it.
  const host = (document.activeElement?.closest('[role="dialog"]') as HTMLElement | null) ?? document.body
  host.appendChild(ta)
  const prev = document.activeElement as HTMLElement | null
  ta.focus()
  ta.select()
  try {
    if (!document.execCommand('copy')) throw new Error('copy refused')
  } finally {
    ta.remove()
    prev?.focus?.()
  }
}
