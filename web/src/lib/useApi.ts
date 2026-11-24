import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError, settleUncertainWrite, toApiError, WRITE_OUTCOME_UNKNOWN } from '@/lib/api'

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
}

const noop = () => {}

/** What a write re-reads when its outcome is unknown; see useWrite. */
export type Settle = (() => Promise<void> | void) | null

/**
 * The one implementation behind useApi and useWrite: state, the unmount
 * guard, the latest-started-call-wins guard, and the settle step. `settleRef`
 * is read when an error arrives, so a run always uses the settle of the latest
 * render, never a stale closure.
 */
function useCall<T>(
  settleRef: { current: Settle } | null,
  isWrite: boolean,
): [ApiState<T>, (fn: () => Promise<T>, options?: RunOptions) => Promise<void>] {
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

  const run = useCallback(
    async (fn: () => Promise<T>, options?: RunOptions) => {
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
        if (isWrite) {
          await settleUncertainWrite(err, settleRef?.current ?? noop)
        } else if (import.meta.env.DEV && err instanceof ApiError && err.code === WRITE_OUTCOME_UNKNOWN) {
          console.error(
            'a write went through useApi (write_outcome_unknown): use useWrite(settle) so the screen re-reads before the error shows',
          )
        }
        if (!mountedRef.current || callId !== callIdRef.current) return
        setState({ status: 'error', data: undefined, error: toApiError(err) })
      }
    },
    [settleRef, isWrite],
  )

  return [state, run]
}

/**
 * The hook for a load: turns an async /api/* read into {status, data, error}.
 * A write does not belong here - it goes through useWrite, which cannot be
 * called without saying what to re-read. In dev, a write_outcome_unknown
 * caught here is logged, because that code only ever comes from a write.
 */
export function useApi<T>(): [ApiState<T>, (fn: () => Promise<T>, options?: RunOptions) => Promise<void>] {
  return useCall<T>(null, false)
}

/**
 * The hook for a write (POST/PUT/PATCH/DELETE): same state and guards as
 * useApi, plus the ADR-043 / #507 rule. When the write's outcome is unknown
 * (`write_outcome_unknown`: the connection died with no answer, so the server
 * may or may not have applied it), `settle` re-reads what the screen shows
 * and is awaited before the error state is set, so the treasurer's retry
 * happens against fresh data. It runs for that one error code only.
 *
 * `settle` is required, with no default, so a write that forgets its re-read
 * fails to compile. Pass `null` only where nothing on screen can go stale:
 * login, register, setup, restore, a password change - the answer is the
 * whole result, and a retry reads nothing the write could have changed.
 * Otherwise pass the screen's reload.
 */
export function useWrite<T>(settle: Settle): [ApiState<T>, (fn: () => Promise<T>) => Promise<void>] {
  const settleRef = useRef<Settle>(settle)
  useEffect(() => {
    settleRef.current = settle
  })
  return useCall<T>(settleRef, true)
}
