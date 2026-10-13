// One typed fetch wrapper for /api/*. Same origin throughout (ADR-001) - no
// base URL, no CORS handling.
//
// Every failure arrives in the server's one error envelope
// (`{"error":{"code":"...","message":"..."}}`, internal/http/errors.go),
// parsed into ApiError so every screen's error state renders it without
// re-parsing JSON.
//
// A network failure (fetch itself threw - no response at all) is told apart
// from an HTTP error response: the connectivity watcher and the copy map both
// need to - a network failure means "we might be offline", an HTTP error does
// not.
export class ApiError extends Error {
  readonly code: string
  /** True when fetch itself threw - no response reached the browser at all. */
  readonly isNetworkError: boolean

  constructor(code: string, message: string, isNetworkError = false) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.isNetworkError = isNetworkError
  }
}

/** The one code used for a request that never got a response. */
export const NETWORK_ERROR_CODE = 'network_error'

/**
 * The code for a WRITE whose fetch threw (connection lost, or the timeout
 * fired). The server may have committed before the response was lost, so the
 * save is uncertain - unlike a read's network_error, which only means "no
 * data".
 */
export const WRITE_OUTCOME_UNKNOWN = 'write_outcome_unknown'

/** How long a write may wait for its response before it is reported as uncertain. */
export const WRITE_TIMEOUT_MS = 20_000

/**
 * The same wait for a write that carries a file (a receipt photo, a backup
 * zip): the body itself has to cross a mobile connection before any response
 * can start, and cutting off an upload that would have landed is the
 * duplicate this code exists to prevent.
 */
export const UPLOAD_TIMEOUT_MS = 120_000

interface ErrorEnvelope {
  error?: {
    code?: string
    message?: string
  }
}

/**
 * A listener notified on every network failure and every successful response,
 * so the connectivity watcher can raise or clear the offline banner from live
 * traffic rather than navigator.onLine alone - that lies about captive
 * portals and about a server that is down.
 */
type ConnectivityListener = (online: boolean) => void

const connectivityListeners = new Set<ConnectivityListener>()

export function onConnectivityChange(listener: ConnectivityListener): () => void {
  connectivityListeners.add(listener)
  return () => connectivityListeners.delete(listener)
}

function notifyConnectivity(online: boolean) {
  for (const listener of connectivityListeners) listener(online)
}

/**
 * Calls an /api/* route and decodes its JSON body as T.
 *
 * Throws ApiError on any failure: the request never reaching a server
 * (isNetworkError: true, code NETWORK_ERROR_CODE), or a non-2xx response,
 * decoded from the server's error envelope when present.
 */
export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? 'GET').toUpperCase()
  const isWrite = method !== 'GET' && method !== 'HEAD'

  let res: Response
  let timeout: ReturnType<typeof setTimeout> | undefined
  try {
    if (isWrite) {
      // A write gets a bounded wait. Our controller carries the timeout and
      // mirrors the caller's signal, so a caller abort stays distinguishable
      // from the timeout.
      const controller = new AbortController()
      const caller = init?.signal
      if (caller?.aborted) controller.abort(caller.reason)
      else caller?.addEventListener('abort', () => controller.abort(caller.reason), { once: true })
      const wait = init?.body instanceof FormData ? UPLOAD_TIMEOUT_MS : WRITE_TIMEOUT_MS
      timeout = setTimeout(() => controller.abort(new Error('write timed out')), wait)
      res = await fetch(path, { ...init, signal: controller.signal })
    } else {
      res = await fetch(path, init)
    }
  } catch (err) {
    notifyConnectivity(false)
    const message = err instanceof Error ? err.message : 'network error'
    // A caller-initiated abort (e.g. unmount) is not an uncertain save: the
    // caller walked away and nothing is waiting on the outcome.
    if (isWrite && !init?.signal?.aborted) throw new ApiError(WRITE_OUTCOME_UNKNOWN, message, true)
    throw new ApiError(NETWORK_ERROR_CODE, message, true)
  } finally {
    clearTimeout(timeout)
  }

  if (!res.ok) {
    // A response - even a failing one - proves the network is up.
    notifyConnectivity(true)
    let envelope: ErrorEnvelope = {}
    try {
      envelope = (await res.json()) as ErrorEnvelope
    } catch {
      // Body wasn't JSON (or was empty) - fall through to the generic code.
    }
    const code = envelope.error?.code ?? 'unknown_error'
    const message = envelope.error?.message ?? `Request failed with status ${res.status}`
    throw new ApiError(code, message)
  }

  notifyConnectivity(true)

  if (res.status === 204) {
    return undefined as T
  }
  return (await res.json()) as T
}

/**
 * For a write that changes no ledger data (sign-in, a restore's inspect step):
 * "the save is uncertain" would mislead there, so the lost response is
 * reported as the plain connection message a read gets. Use as
 * `.catch(plainConnectionError)`.
 */
export function plainConnectionError(err: unknown): never {
  if (err instanceof ApiError && err.code === WRITE_OUTCOME_UNKNOWN) throw new ApiError(NETWORK_ERROR_CODE, err.message, true)
  throw err
}

/**
 * After a write that threw WRITE_OUTCOME_UNKNOWN, re-reads the screen's data so
 * the treasurer sees whether the save landed before she can retry. Any other
 * error is a no-op. A failing reload is logged and swallowed: the original
 * error is the one she must see.
 */
export async function settleUncertainWrite(err: unknown, reload: () => Promise<void> | void): Promise<void> {
  if (!(err instanceof ApiError) || err.code !== WRITE_OUTCOME_UNKNOWN) return
  try {
    await reload()
  } catch (reloadErr) {
    console.warn('reload after an uncertain write failed', reloadErr)
  }
}

/**
 * Calls a route that answers with a file rather than JSON - the backup zips,
 * the report's PDF - and returns it as a Blob. A failure throws the same
 * ApiError apiFetch does.
 */
export async function fetchBlob(path: string): Promise<{ blob: Blob; res: Response }> {
  let res: Response
  try {
    res = await fetch(path)
  } catch (err) {
    notifyConnectivity(false)
    throw new ApiError(NETWORK_ERROR_CODE, err instanceof Error ? err.message : 'network error', true)
  }
  notifyConnectivity(true)

  if (!res.ok) {
    let envelope: ErrorEnvelope = {}
    try {
      envelope = (await res.json()) as ErrorEnvelope
    } catch {
      // Body wasn't JSON (or was empty) - fall through to the generic code.
    }
    const code = envelope.error?.code ?? 'unknown_error'
    const message = envelope.error?.message ?? `Request failed with status ${res.status}`
    throw new ApiError(code, message)
  }

  return { blob: await res.blob(), res }
}

/** How long a saved Blob's object URL outlives the click that opened it. */
const BLOB_URL_TTL_MS = 60_000

/**
 * Hands a Blob to the browser to save under `filename`. The object URL is
 * revoked later, not at once: the installed iOS app opens it in a file viewer
 * that reads the URL after the click returns, and a revoked URL there is an
 * empty viewer.
 */
export function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  setTimeout(() => URL.revokeObjectURL(url), BLOB_URL_TTL_MS)
}
