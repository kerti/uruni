// One typed fetch wrapper for /api/*. Same origin throughout (ADR-001) - no
// base URL, no CORS handling.
//
// Every route answers a failure through the server's one error envelope
// (`{"error":{"code":"...","message":"..."}}`, internal/http/errors.go). This
// module parses that shape into ApiError so every screen's error state can
// render it without re-parsing JSON.
//
// A network failure (fetch itself threw - no response reached the browser at
// all, e.g. the server is down or the connection is gone) is distinguished
// from an HTTP error response (the server answered, just not with 2xx): the
// connectivity watcher and the copy map both need to tell those apart - a
// network failure means "we might be offline", an HTTP error does not.
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

interface ErrorEnvelope {
  error?: {
    code?: string
    message?: string
  }
}

/**
 * A listener notified on every network failure (fetch threw) and every
 * successful response, so the connectivity watcher can raise or clear the
 * offline banner from live traffic rather than navigator.onLine alone -
 * that lies about captive portals and about a server that is down.
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
 * Throws ApiError on any failure:
 * - the request never reaching a server (isNetworkError: true, code
 *   NETWORK_ERROR_CODE)
 * - a non-2xx response, decoded from the server's error envelope when
 *   present (isNetworkError: false)
 */
export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, init)
  } catch (err) {
    notifyConnectivity(false)
    throw new ApiError(NETWORK_ERROR_CODE, err instanceof Error ? err.message : 'network error', true)
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
