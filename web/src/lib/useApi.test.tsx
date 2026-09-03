import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { ApiError } from '@/lib/api'
import { useApi } from '@/lib/useApi'

/** A promise this test resolves by hand, so two calls can be interleaved. */
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('useApi', () => {
  it('walks idle -> loading -> success', async () => {
    const { result } = renderHook(() => useApi<string>())
    expect(result.current[0].status).toBe('idle')

    const call = deferred<string>()
    act(() => {
      void result.current[1](() => call.promise)
    })
    expect(result.current[0].status).toBe('loading')

    await act(async () => {
      call.resolve('ok')
      await call.promise
    })
    expect(result.current[0].status).toBe('success')
    expect(result.current[0].data).toBe('ok')
    expect(result.current[0].error).toBeUndefined()
  })

  it('surfaces an ApiError with its code intact', async () => {
    const { result } = renderHook(() => useApi<string>())

    await act(async () => {
      await result.current[1](() => Promise.reject(new ApiError('not_found', 'nope')))
    })

    expect(result.current[0].status).toBe('error')
    expect(result.current[0].error?.code).toBe('not_found')
    expect(result.current[0].data).toBeUndefined()
  })

  it('wraps a non-ApiError rejection rather than leaking it raw', async () => {
    const { result } = renderHook(() => useApi<string>())

    await act(async () => {
      await result.current[1](() => Promise.reject(new TypeError('boom')))
    })

    expect(result.current[0].status).toBe('error')
    expect(result.current[0].error).toBeInstanceOf(ApiError)
    expect(result.current[0].error?.code).toBe('unknown_error')
  })

  // The race this shape always has: a slow first call must not overwrite a
  // newer one that already answered. Only the most recently *started* call
  // may commit.
  it('ignores a slow response that lost the race to a newer call', async () => {
    const slow = deferred<string>()
    const fast = deferred<string>()
    const { result } = renderHook(() => useApi<string>())

    act(() => {
      void result.current[1](() => slow.promise)
    })
    act(() => {
      void result.current[1](() => fast.promise)
    })

    await act(async () => {
      fast.resolve('newer')
      await fast.promise
    })
    expect(result.current[0].data).toBe('newer')

    // The stale winner lands late and must be dropped on the floor.
    await act(async () => {
      slow.resolve('older')
      await slow.promise
    })
    await waitFor(() => expect(result.current[0].data).toBe('newer'))
    expect(result.current[0].status).toBe('success')
  })

  // RunOptions.silent: a background refresh keeps what is on screen. Both
  // halves matter - Home calls this on every return to the foreground, so
  // dropping to loading would blank the balance on every app switch, and
  // failing loudly would erase it whenever the network blinked.
  it('a silent run keeps the previous data instead of showing loading', async () => {
    const { result } = renderHook(() => useApi<string>())

    await act(async () => {
      await result.current[1](() => Promise.resolve('first'))
    })
    expect(result.current[0].data).toBe('first')

    const call = deferred<string>()
    act(() => {
      void result.current[1](() => call.promise, { silent: true })
    })

    expect(result.current[0].status).toBe('success')
    expect(result.current[0].data).toBe('first')

    await act(async () => {
      call.resolve('second')
      await call.promise
    })
    expect(result.current[0].data).toBe('second')
  })

  it('a failed silent run leaves the last good data on screen', async () => {
    const { result } = renderHook(() => useApi<string>())

    await act(async () => {
      await result.current[1](() => Promise.resolve('first'))
    })

    await act(async () => {
      await result.current[1](() => Promise.reject(new Error('network down')), { silent: true })
    })

    expect(result.current[0].status).toBe('success')
    expect(result.current[0].data).toBe('first')
    expect(result.current[0].error).toBeUndefined()
  })

  // The hook's other guard - not committing after unmount - is deliberately
  // NOT asserted here, because no assertion in this environment can tell it
  // apart from its absence. React 19 makes a setState on an unmounted
  // component a silent no-op (no warning since 18), and renderHook's
  // `result.current` freezes at the last render either way, so a test written
  // against it passes with the guard deleted. Verified by mutation: removing
  // `!mountedRef.current` leaves every test in this file green, while removing
  // the callId check reddens the race test above.
  //
  // The guard stays in the hook - it is correct, and it will matter again the
  // moment a caller holds a ref across unmount - but pretending it is covered
  // would be worse than saying it is not.
  it('resolving after unmount neither throws nor warns', async () => {
    const call = deferred<string>()
    const { result, unmount } = renderHook(() => useApi<string>())

    act(() => {
      void result.current[1](() => call.promise)
    })
    unmount()

    await act(async () => {
      call.resolve('too late')
      await call.promise
    })
  })
})
