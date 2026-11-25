// Copies text to the clipboard.
//
// The legacy execCommand('copy') runs first, synchronously, while the click
// that triggered the copy still counts as a user gesture. Trying the async
// Clipboard API first and falling back after it rejects is unreliable: by the
// time the promise settles, Safari (and embedded browsers that deny clipboard
// permission) no longer treat the call as user-initiated, so the fallback is
// refused too. navigator.clipboard also exists only in secure contexts (HTTPS
// or localhost), not on a plain-HTTP LAN address such as http://192.168.1.10.
export async function copyText(text: string): Promise<void> {
  if (execCopy(text)) return
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text)
    return
  }
  throw new Error('copy refused')
}

function execCopy(text: string): boolean {
  const ta = document.createElement('textarea')
  ta.value = text
  // Inside the viewport (an off-screen element may scroll the page or refuse
  // selection), invisible, and 16px so iOS does not zoom when it is focused.
  ta.style.cssText = 'position:fixed;top:0;left:0;width:1px;height:1px;padding:0;border:0;opacity:0;font-size:16px'
  // Inside an open dialog, focus is trapped there: attach the textarea to it.
  const prev = document.activeElement as HTMLElement | null
  const host = (prev?.closest('[role="dialog"]') as HTMLElement | null) ?? document.body
  host.appendChild(ta)
  try {
    ta.focus({ preventScroll: true })
    ta.select()
    ta.setSelectionRange(0, text.length) // iOS ignores select()
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    ta.remove()
    prev?.focus?.({ preventScroll: true })
  }
}
