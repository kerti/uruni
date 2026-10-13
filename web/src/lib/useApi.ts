import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, settleUncertainWrite } from '@/lib/api'

export type ApiStatus = 'idle' | 'loading' | 'error' | 'success'

export interface ApiState<T> {
  status: ApiStatus
  data: T | undefined
  error: ApiError | undefined
}

/**
 * Turns an async /api/* call into {status, data, error}. Hand-written, not a
 * data-fetching library (no caching, no retries, no offline queue - ADR-008
 * rule 4: offline means unavailable, not queued).
 *
 * Guards two bugs this shape always has: setting state after unmount, and a
 * slow request's response overwriting a newer, faster one's (each call is
 * tagged; only the most recently *started* call may commit).
 */
export interface RunOptions {
  /**
   * Refresh in place: keep whatever is on screen instead of dropping to the
   * loading state, and leave it there if the refresh fails.
   *
   * For a background refresh the user did not ask for - Home re-reading its
   * data when the app returns to the foreground - the default is wrong twice
   * over: it blanks a good screen to "Memuat..." on every app switch, and
   * replaces it with ErrorState if the network is down for that moment. A
   * silent refresh that fails leaves the last good data visible; the
   * OfflineBanner says "butuh koneksi".
   *
   * Never use this for a fetch the user is waiting on: there the loading
   * state is the right one.
   */
  silent?: boolean
  /**
   * For a write: re-reads the screen's data when the write's outcome is
   * unknown, before the error state is set, so the treasurer's retry happens
   * against fresh data. Runs only for that one error code.
   */
  settle?: () => Promise<void> | void
}

const noop = () => {}

export function useApi<T>(): [ApiState<T>, (fn: () => Promise<T>, options?: RunOptions) => Promise<void>] {
  const [state, setState] = useState<ApiState<T>>({
    status: 'idle',
    data: undefined,
    error: undefined,
  })

  const mountedRef = useRef(true)
  const callIdRef = useRef(0)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  const run = useCallback(async (fn: () => Promise<T>, options?: RunOptions) => {
    const callId = ++callIdRef.current
    const silent = options?.silent === true
    if (!silent) setState({ status: 'loading', data: undefined, error: undefined })

    try {
      const data = await fn()
      if (!mountedRef.current || callId !== callIdRef.current) return
      setState({ status: 'success', data, error: undefined })
    } catch (err) {
      if (!mountedRef.current || callId !== callIdRef.current) return
      // A silent refresh that fails changes nothing on screen - see
      // RunOptions.silent. A visible one still surfaces the error.
      if (silent) return
      await settleUncertainWrite(err, options?.settle ?? noop)
      if (!mountedRef.current || callId !== callIdRef.current) return
      const apiError = err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err))
      setState({ status: 'error', data: undefined, error: apiError })
    }
  }, [])

  return [state, run]
}
