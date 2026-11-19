// One WebSocket per tab, topic subscriptions, exponential-backoff reconnect.
// After a reconnect every handler gets a `resync` call so views refetch state.
type Handler = (data: unknown) => void

const handlers = new Map<string, Set<Handler>>()
const resyncers = new Set<() => void>()
let ws: WebSocket | null = null
let retry = 0
let timer: number | undefined
let started = false

function send(op: 'sub' | 'unsub', topic: string) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ op, topic }))
}

function connect() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/api/ws`)
  ws.onopen = () => {
    const wasRetry = retry > 0
    retry = 0
    for (const t of handlers.keys()) send('sub', t)
    if (wasRetry) resyncers.forEach((f) => f())
  }
  ws.onmessage = (ev) => {
    try {
      const m = JSON.parse(ev.data) as { topic: string; data: unknown }
      handlers.get(m.topic)?.forEach((h) => h(m.data))
    } catch { /* ignore malformed */ }
  }
  ws.onclose = () => {
    ws = null
    if (!started) return
    const delay = Math.min(30_000, 500 * 2 ** retry++)
    timer = window.setTimeout(connect, delay)
  }
}

export const realtime = {
  start() { if (!started) { started = true; connect() } },
  stop() { started = false; clearTimeout(timer); ws?.close() },
  subscribe(topic: string, h: Handler, onResync?: () => void): () => void {
    let set = handlers.get(topic)
    if (!set) { set = new Set(); handlers.set(topic, set); send('sub', topic) }
    set.add(h)
    if (onResync) resyncers.add(onResync)
    return () => {
      set!.delete(h)
      if (onResync) resyncers.delete(onResync)
      if (set!.size === 0) { handlers.delete(topic); send('unsub', topic) }
    }
  },
}
