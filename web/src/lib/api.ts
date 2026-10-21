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

import { wallClockMs } from '@/lib/dates'

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

/** The header the server reads on its immutable write routes (ADR-043). */
const IDEMPOTENCY_HEADER = 'Idempotency-Key'

/**
 * How long after its last attempt the client forgets a key (ADR-043). An
 * identical request after that is a new request with a new key. The server
 * keeps its record for 24 hours, which covers only the chain of retries this
 * bounds, never a key older than its last failure plus this.
 */
export const IDEMPOTENCY_FORGET_MS = 15 * 60 * 1000

/**
 * The key of a write whose outcome nobody has seen yet, by the request's
 * identity (method, path and body text), and when it was last attempted
 * (`at`, in wallClockMs() milliseconds: elapsed wall time, so a phone that slept
 * through the window still forgets the key). In memory only: nothing in
 * localStorage or IndexedDB, so ADR-008 and rule 4 stand. A reload forgets it.
 */
const pendingKeys = new Map<string, { key: string; at: number }>()

/** Forgets every remembered key. For tests: module state outlives a case. */
export function resetIdempotencyKeys(): void {
  pendingKeys.clear()
}

/** How many keys are remembered. For tests: the window must drop a body that is
 * never retried, which no request can observe. */
export function heldIdempotencyKeyCount(): number {
  return pendingKeys.size
}

/** 16 random bytes as 32 hex characters. Not crypto.randomUUID, which throws on a non-secure origin (a phone testing against a LAN address). */
function mintIdempotencyKey(): string {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

/**
 * The key this write carries, and what to do with it once its outcome is known.
 * An identical request issued while a key is remembered reuses it (a retry after
 * a lost response, or a twin sent before the first has an outcome); otherwise
 * the key is fresh. A body that is not text (a FormData upload) is never
 * remembered: the server ignores a key on those routes anyway.
 */
function writeKey(method: string, path: string, body: BodyInit | null | undefined) {
  if (body != null && typeof body !== 'string') return { key: mintIdempotencyKey(), settle: () => {}, keep: () => {} }

  const id = `${method} ${path} ${body ?? ''}`
  const now = wallClockMs()
  // Forget every entry past the window, not only this request's: an entry holds
  // the body text (a password change's included), which ADR-043 keeps no longer
  // than a definite answer or 15 minutes, retried or not.
  for (const [other, entry] of pendingKeys) {
    if (now - entry.at >= IDEMPOTENCY_FORGET_MS) pendingKeys.delete(other)
  }
  const held = pendingKeys.get(id)
  let key: string
  if (held && now - held.at < IDEMPOTENCY_FORGET_MS) {
    key = held.key
  } else {
    key = mintIdempotencyKey()
    pendingKeys.set(id, { key, at: now })
  }

  return {
    key,
    // A definite answer (a 2xx or a 4xx proves the request was judged): drop the
    // entry, but only if it still holds this request's own key, so a twin's late
    // answer cannot drop the key of a newer identical request.
    settle: () => {
      if (pendingKeys.get(id)?.key === key) pendingKeys.delete(id)
    },
    // Anything else (a throw, a timeout, a caller's abort, any 5xx) leaves the key
    // for the next identical request and moves `at`. It touches only an entry that
    // still holds its own key and never creates one, so a late failure of an older
    // twin cannot bring back a key a definite answer spent.
    keep: () => {
      if (pendingKeys.get(id)?.key === key) pendingKeys.set(id, { key, at: wallClockMs() })
    },
  }
}

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
  // Every write carries a key (ADR-043); the server honours it on its immutable
  // routes and ignores it on the rest, so nothing here knows which is which. A
  // caller that set the header itself keeps its own and is not tracked.
  const callerKeyed = isWrite && new Headers(init?.headers).has(IDEMPOTENCY_HEADER)
  const idem = isWrite && !callerKeyed ? writeKey(method, path, init?.body) : undefined
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
      const headers = new Headers(init?.headers)
      if (idem) headers.set(IDEMPOTENCY_HEADER, idem.key)
      res = await fetch(path, { ...init, headers, signal: controller.signal })
    } else {
      res = await fetch(path, init)
    }
  } catch (err) {
    idem?.keep()
    notifyConnectivity(false)
    const message = err instanceof Error ? err.message : 'network error'
    // A caller-initiated abort (e.g. unmount) is not an uncertain save: the
    // caller walked away and nothing is waiting on the outcome.
    if (isWrite && !init?.signal?.aborted) throw new ApiError(WRITE_OUTCOME_UNKNOWN, message, true)
    throw new ApiError(NETWORK_ERROR_CODE, message, true)
  } finally {
    clearTimeout(timeout)
  }

  // A 5xx is indefinite even with the API's error envelope: the app may have
  // committed before it failed, or a proxy may have given up on an app that did.
  if (res.status >= 500) idem?.keep()
  else if (!res.ok) idem?.settle()

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
    idem?.settle()
    return undefined as T
  }
  // A 2xx settles the key only once its body is read: a connection that dies
  // mid-body leaves her with no answer, so it is a lost response like any other,
  // and the key stays for her retry to replay.
  let body: T
  try {
    body = (await res.json()) as T
  } catch (err) {
    if (!isWrite) throw err
    idem?.keep()
    throw new ApiError(WRITE_OUTCOME_UNKNOWN, err instanceof Error ? err.message : 'response body lost', true)
  }
  idem?.settle()
  return body
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
