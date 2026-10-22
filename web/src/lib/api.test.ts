import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  ApiError,
  apiFetch,
  heldIdempotencyKeyCount,
  IDEMPOTENCY_FORGET_MS,
  NETWORK_ERROR_CODE,
  plainConnectionError,
  resetIdempotencyKeys,
  settleUncertainWrite,
  WRITE_OUTCOME_UNKNOWN,
  WRITE_TIMEOUT_MS,
  UPLOAD_TIMEOUT_MS,
} from '@/lib/api'

// Module state outlives a case, and a key left by one would be reused by the next.
beforeEach(resetIdempotencyKeys)

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

// ADR-043: every write carries a key; the server honours it on its immutable
// routes. The wrapper remembers a key from the send until a definite response,
// in memory only.
describe('apiFetch idempotency keys', () => {
  const header = 'Idempotency-Key'
  const ok = () => new Response(JSON.stringify({ id: 1 }), { status: 201 })
  const refused = (status = 409) => new Response(JSON.stringify({ error: { code: 'x', message: 'm' } }), { status })

  /** The key each fetch call carried, in order. */
  function sentKeys(fetchMock: ReturnType<typeof vi.fn>): (string | null)[] {
    return (fetchMock.mock.calls as unknown[][]).map((call) => new Headers((call[1] as RequestInit | undefined)?.headers).get(header))
  }

  /** A fetch whose answers are the queued outcomes: a Response, or an Error to throw. */
  function stubFetch(...outcomes: (Response | Error)[]) {
    const queue = [...outcomes]
    const fetchMock = vi.fn(() => {
      const next = queue.shift()
      if (!next) throw new Error('fetch called more often than the test queued')
      return next instanceof Error ? Promise.reject(next) : Promise.resolve(next)
    })
    vi.stubGlobal('fetch', fetchMock)
    return fetchMock
  }

  /** A fetch each of whose calls is answered by hand, so twins can overlap. */
  function stubDeferredFetch() {
    const settlers: { resolve: (r: Response) => void; reject: (e: Error) => void }[] = []
    const fetchMock = vi.fn(
      () =>
        new Promise<Response>((resolve, reject) => {
          settlers.push({ resolve, reject })
        }),
    )
    vi.stubGlobal('fetch', fetchMock)
    return { fetchMock, settlers }
  }

  const lost = () => new TypeError('Failed to fetch')

  it('sends a key of 32 hex characters on every write, and none on a read', async () => {
    const fetchMock = stubFetch(ok(), ok(), ok(), ok())
    await apiFetch('/api/transactions', post)
    await apiFetch('/api/x/1', { method: 'PATCH', body: '{"a":1}' })
    await apiFetch('/api/x/1', { method: 'DELETE' })
    await apiFetch('/api/balances')

    const keys = sentKeys(fetchMock)
    expect(keys.slice(0, 3)).toEqual([
      expect.stringMatching(/^[0-9a-f]{32}$/),
      expect.stringMatching(/^[0-9a-f]{32}$/),
      expect.stringMatching(/^[0-9a-f]{32}$/),
    ])
    expect(new Set(keys.slice(0, 3)).size).toBe(3)
    expect(keys[3]).toBeNull()
    expect((fetchMock.mock.calls as unknown[][])[3]).toEqual(['/api/balances', undefined])
  })

  it('merges with the headers the caller set and leaves Content-Type alone', async () => {
    const fetchMock = stubFetch(ok())
    await apiFetch('/api/transactions', { ...post, headers: { 'Content-Type': 'application/json', 'X-Other': '1' } })

    const sent = new Headers(((fetchMock.mock.calls as unknown[][])[0]![1] as RequestInit).headers)
    expect(sent.get('Content-Type')).toBe('application/json')
    expect(sent.get('X-Other')).toBe('1')
    expect(sent.get(header)).toMatch(/^[0-9a-f]{32}$/)
  })

  it('keeps a key the caller set itself, and does not track it', async () => {
    const fetchMock = stubFetch(lost(), ok())
    const own = 'caller-chosen-key-0123456789'
    await caught(apiFetch('/api/transactions', { ...post, headers: { [header]: own } }))
    await apiFetch('/api/transactions', post)
    expect(sentKeys(fetchMock)[0]).toBe(own)
    expect(sentKeys(fetchMock)[1]).not.toBe(own)
  })

  it('never remembers an upload: a FormData body gets a fresh key each time, and no forced Content-Type', async () => {
    const fetchMock = stubFetch(lost(), ok())
    const form = () => {
      const body = new FormData()
      body.append('file', new Blob(['x']), 'x.jpg')
      return { method: 'POST', body }
    }
    await caught(apiFetch('/api/transactions/1/receipts', form()))
    await apiFetch('/api/transactions/1/receipts', form())

    const [first, second] = sentKeys(fetchMock)
    expect(first).toMatch(/^[0-9a-f]{32}$/)
    expect(second).not.toBe(first)
    expect(new Headers(((fetchMock.mock.calls as unknown[][])[0]![1] as RequestInit).headers).has('Content-Type')).toBe(false)
  })

  describe('a lost or indefinite outcome keeps the key for an identical request', () => {
    it('reuses it after a fetch that threw, and mints another for a different body or path', async () => {
      const fetchMock = stubFetch(lost(), ok(), ok(), ok())
      await caught(apiFetch('/api/transactions', post))
      await apiFetch('/api/transactions', post)
      await apiFetch('/api/transactions', { method: 'POST', body: '{"a":2}' })
      await apiFetch('/api/transfers', post)

      const keys = sentKeys(fetchMock)
      expect(keys[1]).toBe(keys[0])
      expect(keys[2]).not.toBe(keys[0])
      expect(keys[3]).not.toBe(keys[0])
    })

    it('reuses it after the write timed out', async () => {
      vi.useFakeTimers()
      const fetchMock = vi.fn(
        (_path: string, init?: RequestInit) =>
          new Promise<Response>((_resolve, reject) => {
            init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
          }),
      )
      vi.stubGlobal('fetch', fetchMock)
      const pending = caught(apiFetch('/api/transactions', post))
      await vi.advanceTimersByTimeAsync(WRITE_TIMEOUT_MS)
      await pending

      fetchMock.mockImplementationOnce(() => Promise.resolve(ok()))
      await apiFetch('/api/transactions', post)
      const keys = sentKeys(fetchMock as unknown as ReturnType<typeof vi.fn>)
      expect(keys[1]).toBe(keys[0])
    })

    it("reuses it after a caller's abort", async () => {
      const controller = new AbortController()
      const fetchMock = vi.fn((_path: string, init?: RequestInit) => {
        if (fetchMock.mock.calls.length > 1) return Promise.resolve(ok())
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
        })
      })
      vi.stubGlobal('fetch', fetchMock)
      const pending = caught(apiFetch('/api/transactions', { ...post, signal: controller.signal }))
      controller.abort()
      await pending
      await apiFetch('/api/transactions', post)

      const keys = sentKeys(fetchMock as unknown as ReturnType<typeof vi.fn>)
      expect(keys[1]).toBe(keys[0])
    })

    it.each([
      ['a bare 502 from a proxy', () => new Response('Bad Gateway', { status: 502 })],
      ['a 504', () => new Response('', { status: 504 })],
      ['a 500 with the API envelope', () => refused(500)],
      ['a 503', () => refused(503)],
    ])('reuses it after %s', async (_name, response) => {
      const fetchMock = stubFetch(response(), ok())
      await caught(apiFetch('/api/transactions', post))
      await apiFetch('/api/transactions', post)

      const keys = sentKeys(fetchMock)
      expect(keys[1]).toBe(keys[0])
    })
  })

  describe('a definite response drops the key', () => {
    it('after a 2xx the next identical request is a new one', async () => {
      const fetchMock = stubFetch(ok(), ok())
      await apiFetch('/api/transactions', post)
      await apiFetch('/api/transactions', post)

      const keys = sentKeys(fetchMock)
      expect(keys[1]).not.toBe(keys[0])
    })

    it.each([400, 401, 404, 409, 429])('after a %i the next identical request is a new one', async (status) => {
      const fetchMock = stubFetch(refused(status), ok())
      await caught(apiFetch('/api/transactions', post))
      await apiFetch('/api/transactions', post)

      const keys = sentKeys(fetchMock)
      expect(keys[1]).not.toBe(keys[0])
    })

    it('after a lost response, a retry that is answered drops it too', async () => {
      const fetchMock = stubFetch(lost(), refused(409), ok())
      await caught(apiFetch('/api/transactions', post))
      await caught(apiFetch('/api/transactions', post))
      await apiFetch('/api/transactions', post)

      const keys = sentKeys(fetchMock)
      expect(keys[1]).toBe(keys[0])
      expect(keys[2]).not.toBe(keys[0])
    })
  })

  describe('twins', () => {
    it('an identical write sent before the first has an outcome reuses its key', async () => {
      const { fetchMock, settlers } = stubDeferredFetch()
      const a = apiFetch('/api/transactions', post)
      const b = apiFetch('/api/transactions', post)
      settlers[0]!.resolve(ok())
      settlers[1]!.resolve(ok())
      await Promise.all([a, b])

      const keys = sentKeys(fetchMock)
      expect(keys[1]).toBe(keys[0])
    })

    it("an older twin's late failure never re-arms a key a definite answer spent", async () => {
      const { fetchMock, settlers } = stubDeferredFetch()
      const older = caught(apiFetch('/api/transactions', post))
      const newer = apiFetch('/api/transactions', post)
      settlers[1]!.resolve(ok()) // the newer twin is answered: the key is spent
      await newer
      settlers[0]!.reject(lost()) // the older one only now gives up
      await older

      const next = apiFetch('/api/transactions', post)
      settlers[2]!.resolve(ok())
      await next

      const keys = sentKeys(fetchMock)
      expect(keys[2]).not.toBe(keys[0])
    })

    it("an older twin's late answer does not spend the key of a newer identical request", async () => {
      const { fetchMock, settlers } = stubDeferredFetch()
      const older = apiFetch('/api/transactions', post)
      settlers[0]!.resolve(ok()) // answered at once...
      await older
      const newer = caught(apiFetch('/api/transactions', post)) // ...a new request, a new key
      const stale = apiFetch('/api/transactions', post) // a twin of the newer one
      settlers[1]!.reject(lost())
      await newer
      settlers[2]!.resolve(refused(409)) // spends the key it shares with the newer request
      await caught(stale)

      const keys = sentKeys(fetchMock)
      expect(keys[1]).not.toBe(keys[0])
      expect(keys[2]).toBe(keys[1])
    })
  })

  it('treats a 2xx whose body is lost as an unknown outcome, and keeps the key for the retry', async () => {
    const broken = new Response('{"id":', { status: 201 })
    const fetchMock = stubFetch(broken, ok())
    const err = await caught(apiFetch('/api/transactions', post))
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).code).toBe(WRITE_OUTCOME_UNKNOWN)
    await apiFetch('/api/transactions', post)

    const keys = sentKeys(fetchMock)
    expect(keys[1]).toBe(keys[0])
  })

  describe('forgetting', () => {
    it('reuses the key until 15 minutes after the last attempt, and not after', async () => {
      vi.useFakeTimers()
      const fetchMock = stubFetch(lost(), ok())
      await caught(apiFetch('/api/transactions', post))
      vi.advanceTimersByTime(IDEMPOTENCY_FORGET_MS - 1)
      await apiFetch('/api/transactions', post)
      expect(sentKeys(fetchMock)[1]).toBe(sentKeys(fetchMock)[0])

      const late = stubFetch(lost(), ok())
      resetIdempotencyKeys()
      await caught(apiFetch('/api/transactions', post))
      vi.advanceTimersByTime(IDEMPOTENCY_FORGET_MS)
      await apiFetch('/api/transactions', post)
      expect(sentKeys(late)[1]).not.toBe(sentKeys(late)[0])
    })

    it('moves with every failed attempt, so a chain of retries keeps its key past 15 minutes of real time', async () => {
      vi.useFakeTimers()
      const fetchMock = stubFetch(lost(), lost(), ok())
      await caught(apiFetch('/api/transactions', post))
      vi.advanceTimersByTime(IDEMPOTENCY_FORGET_MS - 1000)
      await caught(apiFetch('/api/transactions', post))
      vi.advanceTimersByTime(IDEMPOTENCY_FORGET_MS - 1000)
      await apiFetch('/api/transactions', post)

      const keys = sentKeys(fetchMock)
      expect(keys[2]).toBe(keys[0])
      expect(keys[1]).toBe(keys[0])
    })

    it('is forgotten by the test reset', async () => {
      const fetchMock = stubFetch(lost(), ok())
      await caught(apiFetch('/api/transactions', post))
      resetIdempotencyKeys()
      await apiFetch('/api/transactions', post)

      const keys = sentKeys(fetchMock)
      expect(keys[1]).not.toBe(keys[0])
    })

    it('drops a body never retried once its 15 minutes pass, on the next write of any kind', async () => {
      vi.useFakeTimers()
      stubFetch(lost(), ok())
      await caught(apiFetch('/api/user/password', { method: 'POST', body: '{"current":"a","new":"b"}' }))
      expect(heldIdempotencyKeyCount()).toBe(1)
      vi.advanceTimersByTime(IDEMPOTENCY_FORGET_MS)
      await apiFetch('/api/transactions', post)
      expect(heldIdempotencyKeyCount()).toBe(0)
    })

    it('keeps nothing in browser storage', async () => {
      stubFetch(lost())
      await caught(apiFetch('/api/transactions', post))
      expect(localStorage.length).toBe(0)
      expect(sessionStorage.length).toBe(0)
    })
  })
})
