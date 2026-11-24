import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { ApiError, NETWORK_ERROR_CODE, WRITE_OUTCOME_UNKNOWN } from '@/lib/api'
import { useApi, useWrite } from '@/lib/useApi'

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

  // The race: a slow first call must not overwrite a newer one that already
  // answered. Only the most recently *started* call may commit.
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

  // RunOptions.silent: a background refresh keeps what is on screen. Home
  // calls this on every return to the foreground, so dropping to loading
  // would blank the balance on every app switch, and failing loudly would
  // erase it whenever the network blinked.
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

  // useApi is loads-only: a write_outcome_unknown caught here means a write
  // went through the load hook, which dev builds say out loud.
  it('logs in dev when a write_outcome_unknown reaches the load hook, and still sets the error', async () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const { result } = renderHook(() => useApi<string>())

    await act(async () => {
      await result.current[1](() => Promise.reject(new ApiError(WRITE_OUTCOME_UNKNOWN, 'lost', true)))
    })
    expect(spy).toHaveBeenCalledTimes(1)
    expect(String(spy.mock.calls[0]![0])).toContain('useWrite')
    expect(result.current[0].error?.code).toBe(WRITE_OUTCOME_UNKNOWN)

    spy.mockClear()
    await act(async () => {
      await result.current[1](() => Promise.reject(new ApiError(NETWORK_ERROR_CODE, 'down', true)))
    })
    expect(spy).not.toHaveBeenCalled()
    spy.mockRestore()
  })

  // The hook's other guard - not committing after unmount - is deliberately
  // NOT asserted here: nothing in this environment can tell it from its
  // absence. React 19 makes setState on an unmounted component a silent no-op,
  // and renderHook's `result.current` freezes at the last render either way.
  // Verified by mutation: removing `!mountedRef.current` leaves every test
  // here green, while removing the callId check reddens the race test above.
  // The guard stays - it will matter once a caller holds a ref across
  // unmount - but pretending it is covered would be worse than saying it is
  // not.
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

describe('useWrite', () => {
  const unknown = () => Promise.reject(new ApiError(WRITE_OUTCOME_UNKNOWN, 'lost', true))

  it('runs settle once, before the error state, for write_outcome_unknown only', async () => {
    let seen: string | undefined
    const settle = vi.fn(() => {
      seen = result.current[0].status
    })
    const { result } = renderHook(() => useWrite<string>(settle))

    await act(async () => {
      await result.current[1](unknown)
    })
    expect(settle).toHaveBeenCalledTimes(1)
    // Not yet the error: settle ran first.
    expect(seen).not.toBe('error')
    expect(result.current[0].error?.code).toBe(WRITE_OUTCOME_UNKNOWN)

    for (const err of [new ApiError(NETWORK_ERROR_CODE, 'down', true), new ApiError('invalid_argument', 'bad'), new Error('boom')]) {
      await act(async () => {
        await result.current[1](() => Promise.reject(err))
      })
      expect(result.current[0].status).toBe('error')
    }
    expect(settle).toHaveBeenCalledTimes(1)
  })

  it('does not settle on success', async () => {
    const settle = vi.fn()
    const { result } = renderHook(() => useWrite<string>(settle))
    await act(async () => {
      await result.current[1](() => Promise.resolve('done'))
    })
    expect(settle).not.toHaveBeenCalled()
    expect(result.current[0]).toMatchObject({ status: 'success', data: 'done' })
  })

  it('a null settle runs nothing and still sets the error', async () => {
    const { result } = renderHook(() => useWrite<string>(null))
    await act(async () => {
      await result.current[1](unknown)
    })
    expect(result.current[0].status).toBe('error')
    expect(result.current[0].error?.code).toBe(WRITE_OUTCOME_UNKNOWN)
  })

  it('uses the settle of the latest render, and keeps run stable', async () => {
    const first = vi.fn()
    const second = vi.fn()
    const { result, rerender } = renderHook(({ settle }) => useWrite<string>(settle), { initialProps: { settle: first } })
    const run = result.current[1]
    rerender({ settle: second })
    expect(result.current[1]).toBe(run)

    await act(async () => {
      await run(unknown)
    })
    expect(first).not.toHaveBeenCalled()
    expect(second).toHaveBeenCalledTimes(1)
  })

  it('a swallowed reload failure still lets the original error through', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { result } = renderHook(() => useWrite<string>(() => Promise.reject(new Error('reload failed'))))
    await act(async () => {
      await result.current[1](unknown)
    })
    expect(result.current[0].error?.code).toBe(WRITE_OUTCOME_UNKNOWN)
    warn.mockRestore()
  })

  it('the latest started call wins: an older write failing late does not set error or settle', async () => {
    const slow = deferred<string>()
    const settle = vi.fn()
    const { result } = renderHook(() => useWrite<string>(settle))

    act(() => {
      void result.current[1](() => slow.promise)
    })
    await act(async () => {
      await result.current[1](() => Promise.resolve('newer'))
    })
    await act(async () => {
      slow.reject(new ApiError(WRITE_OUTCOME_UNKNOWN, 'lost', true))
      await slow.promise.catch(() => {})
    })
    expect(settle).not.toHaveBeenCalled()
    expect(result.current[0]).toMatchObject({ status: 'success', data: 'newer' })
  })

  it('a call superseded while settle runs does not set its error', async () => {
    const reload = deferred<void>()
    const { result } = renderHook(() => useWrite<string>(() => reload.promise))

    let first!: Promise<void>
    act(() => {
      first = result.current[1](unknown)
    })
    await waitFor(() => expect(result.current[0].status).toBe('loading'))
    await act(async () => {
      await result.current[1](() => Promise.resolve('newer'))
    })
    await act(async () => {
      reload.resolve()
      await first
    })
    expect(result.current[0]).toMatchObject({ status: 'success', data: 'newer' })
  })
})
