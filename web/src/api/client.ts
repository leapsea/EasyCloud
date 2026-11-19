import { t } from '@/i18n'
// Single HTTP client. Every server error is normalised to ApiError, which may
// carry an `action` telling the UI where the problem can be fixed.
export interface ErrorAction { key?: string; label: string; route: string; focus?: string; role?: string }

/** Button text for a server-suggested fix, translated by its stable key. */
export function actionLabel(a: ErrorAction): string {
  if (a.key) {
    const k = `actions.${a.key}`
    const v = t(k)
    if (v !== k) return v
  }
  return a.label
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    public detail?: unknown,
    public action?: ErrorAction,
    public requestId?: string,
  ) {
    super(code)
  }
}

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    const key = `errors.${e.code}`
    const msg = t(key)
    return msg === key ? e.code : msg
  }
  return e instanceof Error ? e.message : String(e)
}

export async function parseError(res: Response): Promise<ApiError> {
  try {
    const b = await res.json()
    return new ApiError(res.status, b.code ?? 'INTERNAL', b.detail, b.action, b.requestId)
  } catch {
    return new ApiError(res.status, res.status === 404 ? 'NOT_FOUND' : 'INTERNAL')
  }
}

let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(fn: () => void) { onUnauthorized = fn }

export async function request<T>(method: string, url: string, body?: unknown): Promise<T> {
  let res: Response
  try {
    res = await fetch(url, {
      method,
      credentials: 'same-origin',
      headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })
  } catch {
    throw new ApiError(0, 'NETWORK')
  }
  if (!res.ok) {
    const err = await parseError(res)
    if (res.status === 401 && err.code === 'UNAUTHORIZED') onUnauthorized()
    throw err
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

export const qs = (params: Record<string, string>) => new URLSearchParams(params).toString()
