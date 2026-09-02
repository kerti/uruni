import { useCallback, useEffect, useRef, useState } from 'react'

import { ApiError } from '@/lib/api'

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
 * Guards the two bugs this shape always has:
 * - setting state after the component has unmounted
 * - a slow request's response overwriting a newer, faster one's (each call
 *   is tagged; only the most recently *started* call is allowed to commit)
 */
export function useApi<T>(): [ApiState<T>, (fn: () => Promise<T>) => Promise<void>] {
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

  const run = useCallback(async (fn: () => Promise<T>) => {
    const callId = ++callIdRef.current
    setState({ status: 'loading', data: undefined, error: undefined })

    try {
      const data = await fn()
      if (!mountedRef.current || callId !== callIdRef.current) return
      setState({ status: 'success', data, error: undefined })
    } catch (err) {
      if (!mountedRef.current || callId !== callIdRef.current) return
      const apiError =
        err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err))
      setState({ status: 'error', data: undefined, error: apiError })
    }
  }, [])

  return [state, run]
}
