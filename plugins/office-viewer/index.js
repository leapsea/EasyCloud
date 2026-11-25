// Read-only Office preview in the browser, no Document Server needed.
//   docx  rendered by docx-preview (layout, tables, images)
//   xlsx  a small parser of its own: cell values per sheet (no formulas or charts)
//   pptx  the text of each slide
// Runs in the sandboxed viewer frame: only files of this plugin can be loaded
// (no CDN), and every string from the document is inserted as text.

const base = new URL('./', import.meta.url).href
const MAX_BYTES = 50 * 1024 * 1024 // bigger files: download instead
const MAX_ROWS = 1000
const MAX_COLS = 100

const css = `
.ov { min-height: 100%; box-sizing: border-box; font-size: 14px; }
.ov-note { margin: 0; padding: 8px 14px; font-size: 12px; color: #92400e; background: #fef3c7; }
body.dark .ov-note { color: #fcd34d; background: rgba(245,158,11,.15); }
.ov-msg { padding: 32px; text-align: center; color: #64748b; }
.ov-msg button { margin-top: 12px; padding: 6px 14px; border-radius: 8px; border: 1px solid #cbd5e1; background: #fff; cursor: pointer; }
/* docx: white pages on grey, whatever the theme */
.ov-docx { background: #e5e7eb; padding: 16px 0; }
body.dark .ov-docx { background: #1f2937; }
.ov-docx .docx-wrapper { background: transparent !important; padding: 0 !important; }
.ov-docx .docx-wrapper > section.docx { box-shadow: 0 2px 10px rgba(0,0,0,.15); margin: 0 auto 16px !important; }
/* xlsx */
.ov-tabs { position: sticky; top: 0; z-index: 3; display: flex; gap: 2px; overflow-x: auto; padding: 6px 8px 0; background: #f1f5f9; border-bottom: 1px solid #cbd5e1; }
body.dark .ov-tabs { background: #1e293b; border-color: #334155; }
.ov-tabs button { border: 1px solid transparent; border-bottom: none; background: none; padding: 6px 12px; border-radius: 6px 6px 0 0; color: inherit; cursor: pointer; white-space: nowrap; font-size: 13px; }
.ov-tabs button[aria-selected="true"] { background: #fff; border-color: #cbd5e1; font-weight: 600; }
body.dark .ov-tabs button[aria-selected="true"] { background: #0f172a; border-color: #334155; }
.ov-sheet { width: max-content; min-width: 100%; } /* the frame scrolls; sticky headers stick to it */
.ov-sheet table { border-collapse: collapse; font-size: 13px; }
.ov-sheet th, .ov-sheet td { border: 1px solid #e2e8f0; padding: 3px 8px; white-space: pre; max-width: 420px; overflow: hidden; text-overflow: ellipsis; }
body.dark .ov-sheet th, body.dark .ov-sheet td { border-color: #334155; }
.ov-sheet thead th { position: sticky; top: var(--tabs-h, 0px); z-index: 2; background: #f8fafc; color: #64748b; font-weight: 500; }
.ov-sheet tbody th { position: sticky; left: 0; z-index: 1; background: #f8fafc; color: #64748b; font-weight: 500; text-align: right; }
body.dark .ov-sheet thead th, body.dark .ov-sheet tbody th { background: #1e293b; }
.ov-sheet td.num { text-align: right; font-variant-numeric: tabular-nums; }
/* pptx */
.ov-slides { max-width: 880px; margin: 0 auto; padding: 16px; }
.ov-slide { border: 1px solid #e2e8f0; border-radius: 10px; padding: 16px 20px; margin-bottom: 14px; background: #fff; color: #0f172a; box-shadow: 0 1px 3px rgba(0,0,0,.06); }
.ov-slide h3 { margin: 0 0 8px; font-size: 12px; color: #94a3b8; font-weight: 500; }
.ov-slide p { margin: 4px 0; line-height: 1.6; white-space: pre-wrap; }
.ov-slide p.title { font-size: 18px; font-weight: 600; }
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

const ext = (name) => name.toLowerCase().split('.').pop()

// ---------- xlsx ----------
const NS_MAIN = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main'
const NS_REL = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
const parseXml = (s) => new DOMParser().parseFromString(s, 'application/xml')
const byTag = (doc, tag) => Array.from(doc.getElementsByTagNameNS(NS_MAIN, tag))
const text = (n) => Array.from(n.getElementsByTagNameNS(NS_MAIN, 't')).map((t) => t.textContent).join('')

async function readXml(zip, path) {
  const f = zip.file(path)
  return f ? parseXml(await f.async('string')) : null
}

function relTargets(doc, dir) {
  const out = {}
  if (!doc) return out
  for (const r of Array.from(doc.getElementsByTagName('Relationship'))) {
    const t = r.getAttribute('Target')
    out[r.getAttribute('Id')] = t.startsWith('/') ? t.slice(1) : dir + t
  }
  return out
}

// Date formats: built-in ids, or a custom code with date/time letters outside quotes.
const BUILTIN_DATES = new Set([14, 15, 16, 17, 18, 19, 20, 21, 22, 45, 46, 47])
function dateStyles(styles) {
  const out = []
  if (!styles) return out
  const custom = {}
  for (const f of byTag(styles, 'numFmt')) custom[+f.getAttribute('numFmtId')] = f.getAttribute('formatCode') || ''
  const xfs = byTag(styles, 'cellXfs')[0]
  if (!xfs) return out
  for (const xf of Array.from(xfs.getElementsByTagNameNS(NS_MAIN, 'xf'))) {
    const id = +xf.getAttribute('numFmtId')
    const code = (custom[id] || '').replace(/"[^"]*"|\[[^\]]*\]|\\./g, '')
    out.push(BUILTIN_DATES.has(id) || /[dmyhs]/i.test(code) ? (/[hs]/i.test(code) || [18, 19, 20, 21, 22, 45, 46, 47].includes(id) ? 'datetime' : 'date') : '')
  }
  return out
}

function excelDate(serial, date1904, withTime) {
  const ms = Math.round((serial - (date1904 ? -1462 : 0) - 25569) * 86400000)
  const d = new Date(ms)
  if (isNaN(d)) return String(serial)
  const p = (n) => String(n).padStart(2, '0')
  const day = `${d.getUTCFullYear()}-${p(d.getUTCMonth() + 1)}-${p(d.getUTCDate())}`
  return withTime ? `${day} ${p(d.getUTCHours())}:${p(d.getUTCMinutes())}:${p(d.getUTCSeconds())}` : day
}

const colIndex = (ref) => { let n = 0; for (const ch of ref.replace(/\d+/g, '')) n = n * 26 + ch.charCodeAt(0) - 64; return n - 1 }
const colName = (i) => { let s = ''; i++; while (i > 0) { const m = (i - 1) % 26; s = String.fromCharCode(65 + m) + s; i = Math.floor((i - 1) / 26) } return s }

async function parseXlsx(zip) {
  const wb = await readXml(zip, 'xl/workbook.xml')
  if (!wb) throw new Error('not an xlsx workbook')
  const rels = relTargets(await readXml(zip, 'xl/_rels/workbook.xml.rels'), 'xl/')
  const date1904 = byTag(wb, 'workbookPr')[0]?.getAttribute('date1904') === '1'
  const sst = await readXml(zip, 'xl/sharedStrings.xml')
  const strings = sst ? byTag(sst, 'si').map(text) : []
  const dates = dateStyles(await readXml(zip, 'xl/styles.xml'))
  const sheets = []
  for (const s of byTag(wb, 'sheet')) {
    const name = s.getAttribute('name')
    const target = rels[s.getAttributeNS(NS_REL, 'id')]
    const doc = target && await readXml(zip, target)
    const rows = []
    let truncated = false, width = 0
    if (doc) {
      for (const r of byTag(doc, 'row')) {
        const ri = (+r.getAttribute('r') || rows.length + 1) - 1
        if (ri >= MAX_ROWS) { truncated = true; break }
        const row = rows[ri] = rows[ri] || []
        for (const c of Array.from(r.getElementsByTagNameNS(NS_MAIN, 'c'))) {
          const ref = c.getAttribute('r')
          const ci = ref ? colIndex(ref) : row.length
          if (ci >= MAX_COLS) { truncated = true; continue }
          const t = c.getAttribute('t')
          const v = c.getElementsByTagNameNS(NS_MAIN, 'v')[0]?.textContent ?? ''
          let val = v, num = false
          if (t === 's') val = strings[+v] ?? ''
          else if (t === 'inlineStr') val = text(c)
          else if (t === 'b') val = v === '1' ? 'TRUE' : 'FALSE'
          else if (t === 'str' || t === 'e') val = v
          else if (v !== '') {
            const n = Number(v)
            const kind = dates[+(c.getAttribute('s') || 0)]
            if (kind && !isNaN(n)) val = excelDate(n, date1904, kind === 'datetime')
            else { val = isNaN(n) ? v : String(parseFloat(n.toPrecision(15))); num = !isNaN(n) }
          }
          row[ci] = { val, num }
          width = Math.max(width, ci + 1)
        }
      }
    }
    sheets.push({ name, rows, width, truncated })
  }
  return sheets
}

function renderSheet(sheet) {
  const wrap = el('div', { class: 'ov-sheet' })
  if (sheet.truncated) wrap.append(el('p', { class: 'ov-note', text: `表格较大，只显示前 ${MAX_ROWS} 行、前 ${MAX_COLS} 列。完整内容请下载后查看。` }))
  if (!sheet.width) { wrap.append(el('div', { class: 'ov-msg', text: '（空工作表）' })); return wrap }
  const head = el('tr', {}, [el('th')])
  for (let c = 0; c < sheet.width; c++) head.append(el('th', { text: colName(c) }))
  const body = el('tbody')
  for (let r = 0; r < sheet.rows.length; r++) {
    const row = sheet.rows[r] || []
    const tr = el('tr', {}, [el('th', { text: String(r + 1) })])
    for (let c = 0; c < sheet.width; c++) {
      const cell = row[c]
      tr.append(el('td', cell ? { text: cell.val, class: cell.num ? 'num' : '', title: cell.val } : {}))
    }
    body.append(tr)
  }
  wrap.append(el('table', {}, [el('thead', {}, [head]), body]))
  return wrap
}

function renderWorkbook(root, sheets) {
  const tabs = el('div', { class: 'ov-tabs', role: 'tablist' })
  const area = el('div')
  const show = (i) => {
    area.replaceChildren(renderSheet(sheets[i]))
    Array.from(tabs.children).forEach((b, j) => b.setAttribute('aria-selected', String(i === j)))
  }
  sheets.forEach((s, i) => {
    const b = el('button', { role: 'tab', text: s.name })
    b.addEventListener('click', () => show(i))
    tabs.append(b)
  })
  root.append(tabs, area)
  // Column headers stick right below the sheet tabs.
  root.style.setProperty('--tabs-h', tabs.offsetHeight + 'px')
  if (sheets.length) show(0)
}

// ---------- pptx ----------
const NS_A = 'http://schemas.openxmlformats.org/drawingml/2006/main'
const NS_P = 'http://schemas.openxmlformats.org/presentationml/2006/main'

async function parsePptx(zip) {
  const pres = await readXml(zip, 'ppt/presentation.xml')
  if (!pres) throw new Error('not a pptx presentation')
  const rels = relTargets(await readXml(zip, 'ppt/_rels/presentation.xml.rels'), 'ppt/')
  const slides = []
  for (const id of Array.from(pres.getElementsByTagNameNS(NS_P, 'sldId'))) {
    const doc = await readXml(zip, rels[id.getAttributeNS(NS_REL, 'id')])
    if (!doc) continue
    const paras = []
    for (const sp of Array.from(doc.getElementsByTagNameNS(NS_P, 'sp'))) {
      const ph = sp.getElementsByTagNameNS(NS_P, 'ph')[0]?.getAttribute('type') || ''
      for (const p of Array.from(sp.getElementsByTagNameNS(NS_A, 'p'))) {
        const t = Array.from(p.getElementsByTagNameNS(NS_A, 't')).map((x) => x.textContent).join('')
        if (t.trim()) paras.push({ text: t, title: ph === 'title' || ph === 'ctrTitle' })
      }
    }
    slides.push(paras)
  }
  return slides
}

function renderSlides(root, slides) {
  const list = el('div', { class: 'ov-slides' })
  list.append(el('p', { class: 'ov-note', text: '演示文稿仅显示每页的文字内容；排版、图片和动画请下载后查看。' }))
  slides.forEach((paras, i) => {
    const card = el('section', { class: 'ov-slide' }, [el('h3', { text: `第 ${i + 1} 页` })])
    if (!paras.length) card.append(el('p', { text: '（本页没有文字）' }))
    for (const p of paras) card.append(el('p', { text: p.text, class: p.title ? 'title' : '' }))
    list.append(card)
  })
  root.append(list)
}

// ---------- entry ----------
export default {
  async mount(root, ctx) {
    const style = document.createElement('style')
    style.textContent = css
    document.head.appendChild(style)
    const box = el('div', { class: 'ov' })
    root.append(box)
    const message = (msg) => {
      const m = el('div', { class: 'ov-msg' }, [el('div', { text: msg })])
      const b = el('button', { text: '下载文件' })
      b.addEventListener('click', () => ctx.download())
      m.append(b)
      box.replaceChildren(m)
    }
    if (ctx.file.size > MAX_BYTES) { message('文件超过 50 MB，无法在线预览，请下载后查看。'); return }
    const kind = ext(ctx.file.name)
    await loadScript(base + 'vendor/jszip.min.js')
    const blob = await ctx.fetchBlob()
    try {
      if (kind.startsWith('doc')) {
        await loadScript(base + 'vendor/docx-preview.min.js')
        const pages = el('div', { class: 'ov-docx' })
        box.append(pages)
        await window.docx.renderAsync(blob, pages, null, { inWrapper: true, breakPages: true, ignoreLastRenderedPageBreak: true, useBase64URL: true, renderHeaders: true, renderFooters: true })
      } else if (kind.startsWith('xl')) {
        renderWorkbook(box, await parseXlsx(await window.JSZip.loadAsync(blob)))
      } else {
        renderSlides(box, await parsePptx(await window.JSZip.loadAsync(blob)))
      }
    } catch (e) {
      message('无法预览这个文件（' + (e && e.message ? e.message : String(e)) + '）。它可能已损坏或使用了不支持的格式，请下载后查看。')
    }
  },
}
