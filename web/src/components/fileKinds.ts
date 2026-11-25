// Text files the built-in viewer opens. MIME detection misses many of them
// (".log", ".conf" often have none), so common extensions count too.
const TEXT_EXT = new Set(['txt', 'log', 'md', 'markdown', 'csv', 'tsv', 'json', 'jsonl', 'xml', 'yaml', 'yml', 'toml', 'ini', 'conf', 'cfg', 'env',
  'sh', 'bash', 'zsh', 'py', 'go', 'js', 'ts', 'mjs', 'vue', 'css', 'scss', 'html', 'htm', 'sql', 'java', 'kt', 'c', 'h', 'cpp', 'hpp', 'rs', 'rb', 'php', 'srt', 'vtt', 'ass', 'nfo'])

export function isTextFile(name: string, mime?: string): boolean {
  const m = (mime ?? '').toLowerCase()
  if (m.startsWith('text/') || m === 'application/json' || m === 'application/xml') return true
  const ext = name.includes('.') ? name.slice(name.lastIndexOf('.') + 1).toLowerCase() : ''
  return TEXT_EXT.has(ext)
}

export const isPdf = (name: string, mime?: string) => (mime ?? '').includes('pdf') || name.toLowerCase().endsWith('.pdf')
