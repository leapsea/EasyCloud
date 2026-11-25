// Source code preview: syntax highlighting (highlight.js), line numbers, wrap
// toggle. Large files are read only up to MAX_BYTES with a Range request so
// a big JSON or SQL dump cannot freeze the tab. Code is shown as text: HTML
// is never rendered, and highlight.js escapes everything it outputs.

const base = new URL('./', import.meta.url).href
const MAX_BYTES = 1024 * 1024
const EXTRA = ['dockerfile', 'powershell', 'scala', 'dart', 'groovy', 'nginx', 'properties'] // beyond the common bundle

// Extension -> highlight.js language.
const LANG = {
  html: 'xml', htm: 'xml', xhtml: 'xml', vue: 'xml', svelte: 'xml', xml: 'xml', xsd: 'xml', xsl: 'xml', plist: 'xml',
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript', ts: 'typescript', mts: 'typescript', cts: 'typescript', tsx: 'typescript',
  css: 'css', scss: 'scss', sass: 'scss', less: 'less', java: 'java', kt: 'kotlin', kts: 'kotlin', groovy: 'groovy', gradle: 'groovy', scala: 'scala',
  go: 'go', py: 'python', rb: 'ruby', php: 'php', pl: 'perl', pm: 'perl', lua: 'lua', r: 'r', dart: 'dart',
  c: 'c', h: 'c', cc: 'cpp', cpp: 'cpp', cxx: 'cpp', hpp: 'cpp', hh: 'cpp', cs: 'csharp', rs: 'rust', swift: 'swift', m: 'objectivec', mm: 'objectivec', vb: 'vbnet',
  sh: 'bash', bash: 'bash', zsh: 'bash', ps1: 'powershell', bat: 'dos', cmd: 'dos',
  sql: 'sql', json: 'json', jsonc: 'json', yaml: 'yaml', yml: 'yaml', toml: 'ini', ini: 'ini', properties: 'properties',
  graphql: 'graphql', gql: 'graphql', dockerfile: 'dockerfile', mk: 'makefile', makefile: 'makefile', diff: 'diff', patch: 'diff', nginx: 'nginx', wat: 'wasm',
}

const css = `
.cv { display: flex; flex-direction: column; min-height: 100%; font-size: 13px; }
.cv-bar { position: sticky; top: 0; z-index: 2; display: flex; flex-wrap: wrap; align-items: center; gap: 10px; padding: 6px 12px; font-size: 12px;
  background: #f8fafc; border-bottom: 1px solid #e2e8f0; color: #64748b; }
body.dark .cv-bar { background: #1e293b; border-color: #334155; color: #94a3b8; }
.cv-bar .warn { color: #b45309; } body.dark .cv-bar .warn { color: #fcd34d; }
.cv-bar label { display: inline-flex; align-items: center; gap: 4px; cursor: pointer; margin-left: auto; }
.cv-bar button { padding: 2px 10px; border-radius: 6px; border: 1px solid #cbd5e1; background: transparent; color: inherit; cursor: pointer; font-size: 12px; }
.cv-body { display: flex; align-items: flex-start; }
.cv-gutter, .cv-code { margin: 0; padding: 10px 0; font: 12.5px/1.6 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
.cv-gutter { position: sticky; left: 0; flex: none; padding: 10px 10px 10px 12px; text-align: right; color: #94a3b8; user-select: none;
  background: #f8fafc; border-right: 1px solid #e2e8f0; }
body.dark .cv-gutter { background: #111827; border-color: #334155; color: #64748b; }
.cv-code { display: block; padding-left: 14px; padding-right: 14px; white-space: pre; tab-size: 4; background: transparent !important; overflow: visible !important; }
.cv.wrap .cv-code { white-space: pre-wrap; word-break: break-word; }
.cv.wrap .cv-gutter { display: none; }
.cv-code.hljs { padding: 10px 14px; }
`

function el(tag, props = {}, children = []) {
  const e = document.createElement(tag)
  for (const [k, v] of Object.entries(props)) {
    if (k === 'text') e.textContent = v
    else if (k === 'class') e.className = v
    else e.setAttribute(k, v)
  }
  for (const c of children) e.append(c)
  return e
}

function loadScript(src) {
  return new Promise((resolve, reject) => {
    const s = document.createElement('script')
    s.src = src
    s.onload = () => resolve()
    s.onerror = () => reject(new Error('cannot load ' + src))
    document.head.appendChild(s)
  })
}

async function readText(ctx) {
  const size = ctx.file.size || 0
  if (size <= MAX_BYTES) return { text: await ctx.fetchText(), truncated: false }
  const res = await fetch(ctx.url, { headers: { Range: `bytes=0-${MAX_BYTES - 1}` } })
  if (!res.ok) throw new Error('HTTP ' + res.status)
  let buf = new Uint8Array(await res.arrayBuffer())
  if (res.status !== 206) buf = buf.subarray(0, MAX_BYTES)
  let text = new TextDecoder('utf-8').decode(buf)
  text = text.slice(0, text.lastIndexOf('\n') + 1) || text // end on a whole line
  return { text, truncated: true }
}

const fmtSize = (n) => (n < 1024 * 1024 ? (n / 1024).toFixed(1) + ' KB' : (n / 1024 / 1024).toFixed(1) + ' MB')

export default {
  async mount(root, ctx) {
    const style = document.createElement('style')
    style.textContent = css
    document.head.appendChild(style)
    const theme = el('link', { rel: 'stylesheet', href: base + (ctx.theme === 'dark' ? 'vendor/github-dark.min.css' : 'vendor/github.min.css') })
    document.head.appendChild(theme)

    const [{ text, truncated }] = await Promise.all([
      readText(ctx),
      loadScript(base + 'vendor/highlight.min.js').then(() => Promise.all(EXTRA.map((l) => loadScript(base + `vendor/languages/${l}.min.js`).catch(() => {})))),
    ])
    const hljs = window.hljs
    const ext = ctx.file.name.toLowerCase().split('.').pop()
    const name = ctx.file.name.toLowerCase()
    let lang = LANG[ext] || (name === 'dockerfile' ? 'dockerfile' : name === 'makefile' ? 'makefile' : '')
    if (lang && !hljs.getLanguage(lang)) lang = ''

    const code = el('code', { class: 'cv-code hljs' })
    let shown = lang || 'plaintext'
    if (lang) code.innerHTML = hljs.highlight(text, { language: lang, ignoreIllegals: true }).value
    else if (text.length < 200 * 1024) { const r = hljs.highlightAuto(text); code.innerHTML = r.value; shown = r.language || 'plaintext' }
    else code.textContent = text

    const lines = text.endsWith('\n') ? text.split('\n').length - 1 : text.split('\n').length
    const gutter = el('pre', { class: 'cv-gutter', 'aria-hidden': 'true', text: Array.from({ length: lines }, (_, i) => i + 1).join('\n') })

    const box = el('div', { class: 'cv' })
    const wrap = el('input', { type: 'checkbox' })
    wrap.addEventListener('change', () => box.classList.toggle('wrap', wrap.checked))
    const bar = el('div', { class: 'cv-bar' }, [
      el('span', { text: shown }),
      el('span', { text: `${lines.toLocaleString()} 行 · ${fmtSize(ctx.file.size || text.length)}` }),
    ])
    if (truncated) {
      bar.append(el('span', { class: 'warn', text: `文件较大，仅显示开头 ${fmtSize(MAX_BYTES)}` }))
      const dl = el('button', { text: '下载完整文件' })
      dl.addEventListener('click', () => ctx.download())
      bar.append(dl)
    }
    bar.append(el('label', {}, [wrap, document.createTextNode('自动换行')]))
    box.append(bar, el('div', { class: 'cv-body' }, [gutter, el('pre', { class: 'cv-code-wrap', style: 'margin:0;flex:1;min-width:0' }, [code])]))
    root.append(box)
  },
}
