import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  ApiError,
  apiFetch,
  NETWORK_ERROR_CODE,
  plainConnectionError,
  settleUncertainWrite,
  WRITE_OUTCOME_UNKNOWN,
  WRITE_TIMEOUT_MS,
  UPLOAD_TIMEOUT_MS,
} from '@/lib/api'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

const post = { method: 'POST', body: '{}' }

async function caught(promise: Promise<unknown>): Promise<ApiError> {
  try {
    await promise
  } catch (err) {
    return err as ApiError
  }
  throw new Error('expected the call to throw')
}

describe('apiFetch on a lost response', () => {
  it('reports a write whose fetch throws as write_outcome_unknown', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    const err = await caught(apiFetch('/api/transactions', post))
    expect(err.code).toBe(WRITE_OUTCOME_UNKNOWN)
    expect(err.isNetworkError).toBe(true)
  })

  it('treats PATCH and DELETE as writes too', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    expect((await caught(apiFetch('/api/x/1', { method: 'PATCH' }))).code).toBe(WRITE_OUTCOME_UNKNOWN)
    expect((await caught(apiFetch('/api/x/1', { method: 'DELETE' }))).code).toBe(WRITE_OUTCOME_UNKNOWN)
  })

  it('keeps network_error for a read', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    const err = await caught(apiFetch('/api/balances'))
    expect(err.code).toBe(NETWORK_ERROR_CODE)
    expect(err.isNetworkError).toBe(true)
  })

  it('gives a write a bounded wait and reports the timeout as write_outcome_unknown', async () => {
    vi.useFakeTimers()
    // A fetch that never answers and only ends when its signal aborts.
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_path: string, init?: RequestInit) =>
          new Promise((_resolve, reject) => {
            init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
          }),
      ),
    )
    const pending = caught(apiFetch('/api/transactions', post))
    await vi.advanceTimersByTimeAsync(WRITE_TIMEOUT_MS)
    const err = await pending
    expect(err.code).toBe(WRITE_OUTCOME_UNKNOWN)
  })

  it('gives an upload the longer wait, so a slow photo is not cut off at the write timeout', async () => {
    vi.useFakeTimers()
    let aborted = false
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_path: string, init?: RequestInit) =>
          new Promise((_resolve, reject) => {
            init?.signal?.addEventListener('abort', () => {
              aborted = true
              reject(new DOMException('aborted', 'AbortError'))
            })
          }),
      ),
    )
    const pending = caught(apiFetch('/api/receipts', { method: 'POST', body: new FormData() }))
    await vi.advanceTimersByTimeAsync(WRITE_TIMEOUT_MS + 1)
    expect(aborted).toBe(false)
    await vi.advanceTimersByTimeAsync(UPLOAD_TIMEOUT_MS)
    const err = await pending
    expect(err.code).toBe(WRITE_OUTCOME_UNKNOWN)
  })

  it('does not report a caller abort as an uncertain save', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        (_path: string, init?: RequestInit) =>
          new Promise((_resolve, reject) => {
            init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
          }),
      ),
    )
    const controller = new AbortController()
    const pending = caught(apiFetch('/api/transactions', { ...post, signal: controller.signal }))
    controller.abort()
    const err = await pending
    expect(err.code).toBe(NETWORK_ERROR_CODE)
    expect(err.code).not.toBe(WRITE_OUTCOME_UNKNOWN)
  })

  it('still lets a write that answers in time through, and clears its timer', async () => {
    vi.useFakeTimers()
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ id: 1 }), { status: 200 })))
    await expect(apiFetch('/api/transactions', post)).resolves.toEqual({ id: 1 })
    expect(vi.getTimerCount()).toBe(0)
  })
})

describe('settleUncertainWrite', () => {
  let reload: ReturnType<typeof vi.fn<() => Promise<void>>>
  beforeEach(() => {
    reload = vi.fn<() => Promise<void>>().mockResolvedValue(undefined)
  })

  it('reloads for write_outcome_unknown only', async () => {
    await settleUncertainWrite(new ApiError(WRITE_OUTCOME_UNKNOWN, 'x', true), reload)
    expect(reload).toHaveBeenCalledTimes(1)

    await settleUncertainWrite(new ApiError(NETWORK_ERROR_CODE, 'x', true), reload)
    await settleUncertainWrite(new ApiError('invalid_argument', 'x'), reload)
    await settleUncertainWrite(new Error('boom'), reload)
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('swallows a failing reload so the original error still shows', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    reload.mockRejectedValue(new Error('still down'))
    await expect(settleUncertainWrite(new ApiError(WRITE_OUTCOME_UNKNOWN, 'x', true), reload)).resolves.toBeUndefined()
    warn.mockRestore()
  })
})

describe('plainConnectionError', () => {
  it('turns an uncertain write into the plain connection error and passes anything else through', () => {
    expect(() => plainConnectionError(new ApiError(WRITE_OUTCOME_UNKNOWN, 'x', true))).toThrow(
      expect.objectContaining({ code: NETWORK_ERROR_CODE }),
    )
    const other = new ApiError('invalid_credentials', 'x')
    expect(() => plainConnectionError(other)).toThrow(other)
  })
})
