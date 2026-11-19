// Minimal, dependency-free Markdown renderer. All text is HTML-escaped before
// formatting, and only http(s)/relative links are produced, so file content can
// never inject markup (the viewer also runs in a sandboxed iframe).
const esc = (s) => s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c])

function safeUrl(u) {
  const s = u.trim()
  return /^(https?:\/\/|\/|\.\.?\/|#)/i.test(s) && !/^javascript:/i.test(s) ? s : '#'
}

function inline(s) {
  const codes = []
  s = s.replace(/`([^`]+)`/g, (_, c) => { codes.push(c); return `\u0000${codes.length - 1}\u0000` })
  s = esc(s)
  s = s.replace(/!\[([^\]]*)\]\(([^)\s]+)\)/g, (_, alt, src) => `<img alt="${alt}" src="${esc(safeUrl(src))}">`)
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, t, href) => `<a href="${esc(safeUrl(href))}" target="_blank" rel="noopener noreferrer">${t}</a>`)
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>').replace(/(^|[^*])\*([^*]+)\*/g, '$1<em>$2</em>')
  s = s.replace(/~~([^~]+)~~/g, '<del>$1</del>')
  return s.replace(/\u0000(\d+)\u0000/g, (_, i) => `<code>${esc(codes[+i])}</code>`)
}

export function render(md) {
  const lines = md.replace(/\r\n?/g, '\n').split('\n')
  const out = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    let m
    if ((m = line.match(/^```\s*([\w-]*)/))) {
      const buf = []
      i++
      while (i < lines.length && !lines[i].startsWith('```')) buf.push(lines[i++])
      i++
      out.push(`<pre><code class="lang-${esc(m[1])}">${esc(buf.join('\n'))}</code></pre>`)
      continue
    }
    if ((m = line.match(/^(#{1,6})\s+(.*)$/))) { out.push(`<h${m[1].length}>${inline(m[2])}</h${m[1].length}>`); i++; continue }
    if (/^(\s*[-*_]){3,}\s*$/.test(line)) { out.push('<hr>'); i++; continue }
    if (/^>\s?/.test(line)) {
      const buf = []
      while (i < lines.length && /^>\s?/.test(lines[i])) buf.push(lines[i++].replace(/^>\s?/, ''))
      out.push(`<blockquote>${render(buf.join('\n'))}</blockquote>`)
      continue
    }
    if (/^\s*([-*+]|\d+\.)\s+/.test(line)) {
      const ordered = /^\s*\d+\./.test(line)
      const items = []
      while (i < lines.length && /^\s*([-*+]|\d+\.)\s+/.test(lines[i])) {
        const t = lines[i++].replace(/^\s*([-*+]|\d+\.)\s+/, '')
        const task = t.match(/^\[([ xX])\]\s+(.*)$/)
        items.push(task ? `<li class="task"><input type="checkbox" disabled ${task[1] !== ' ' ? 'checked' : ''}> ${inline(task[2])}</li>` : `<li>${inline(t)}</li>`)
      }
      out.push(ordered ? `<ol>${items.join('')}</ol>` : `<ul>${items.join('')}</ul>`)
      continue
    }
    if (line.includes('|') && i + 1 < lines.length && /^\s*\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?\s*$/.test(lines[i + 1])) {
      const cells = (l) => l.trim().replace(/^\||\|$/g, '').split('|').map((c) => c.trim())
      const head = cells(line)
      i += 2
      const rows = []
      while (i < lines.length && lines[i].includes('|') && lines[i].trim()) rows.push(cells(lines[i++]))
      out.push(`<table><thead><tr>${head.map((h) => `<th>${inline(h)}</th>`).join('')}</tr></thead><tbody>${rows.map((r) => `<tr>${r.map((c) => `<td>${inline(c)}</td>`).join('')}</tr>`).join('')}</tbody></table>`)
      continue
    }
    if (!line.trim()) { i++; continue }
    const buf = []
    while (i < lines.length && lines[i].trim() && !/^(#{1,6}\s|```|>|\s*([-*+]|\d+\.)\s)/.test(lines[i])) buf.push(lines[i++])
    out.push(`<p>${inline(buf.join(' '))}</p>`)
  }
  return out.join('\n')
}

const css = `
.md { max-width: 860px; margin: 0 auto; padding: 24px 28px 64px; line-height: 1.7; font-size: 15px; }
.md h1, .md h2 { border-bottom: 1px solid rgba(128,128,128,.25); padding-bottom: .3em; }
.md pre { background: rgba(128,128,128,.12); padding: 12px 14px; border-radius: 8px; overflow: auto; }
.md code { background: rgba(128,128,128,.15); padding: .1em .35em; border-radius: 4px; font-size: .9em; }
.md pre code { background: none; padding: 0; }
.md blockquote { margin: 0; padding: 0 1em; color: #64748b; border-left: 4px solid rgba(128,128,128,.35); }
.md table { border-collapse: collapse; } .md th, .md td { border: 1px solid rgba(128,128,128,.35); padding: 6px 12px; }
.md img { max-width: 100%; } .md a { color: #2563eb; } .md li.task { list-style: none; margin-left: -1.3em; }
`

export default {
  async mount(el, ctx) {
    const style = document.createElement('style')
    style.textContent = css
    document.head.appendChild(style)
    const text = await ctx.fetchText()
    const div = document.createElement('article')
    div.className = 'md'
    div.innerHTML = render(text)
    el.appendChild(div)
  },
}
