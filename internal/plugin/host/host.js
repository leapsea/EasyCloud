// Sandbox host for viewer plugins. This page runs in an iframe with
// sandbox="allow-scripts" (opaque origin): no cookies, no access to the app.
// The parent sends one "init" message; the plugin ES module is imported and mounted.
let started = false

function send(msg) { parent.postMessage({ source: 'easycloud-viewer', ...msg }, '*') }

window.addEventListener('message', async (ev) => {
  if (ev.source !== window.parent || started) return
  const m = ev.data
  if (!m || m.type !== 'init' || typeof m.entry !== 'string' || !m.entry.startsWith('/plugins/')) return
  started = true
  document.body.classList.toggle('dark', m.theme === 'dark')
  const root = document.getElementById('root')
  const file = Object.freeze({ ...m.file })
  const ctx = Object.freeze({
    file,
    theme: m.theme,
    url: file.url,
    fetchText: () => fetch(file.url).then((r) => { if (!r.ok) throw new Error('HTTP ' + r.status); return r.text() }),
    fetchBlob: () => fetch(file.url).then((r) => { if (!r.ok) throw new Error('HTTP ' + r.status); return r.blob() }),
    download: () => send({ type: 'download' }),
    close: () => send({ type: 'close' }),
  })
  try {
    const mod = await import(m.entry)
    const plugin = mod.default ?? mod
    if (typeof plugin.mount !== 'function') throw new Error('plugin does not export mount()')
    await plugin.mount(root, ctx)
    send({ type: 'ready' })
  } catch (e) {
    root.innerHTML = ''
    const div = document.createElement('div')
    div.className = 'ec-error'
    div.textContent = '查看器加载失败：' + (e && e.message ? e.message : String(e))
    root.appendChild(div)
    send({ type: 'error', message: String(e && e.message ? e.message : e) })
  }
})
send({ type: 'loaded' })
