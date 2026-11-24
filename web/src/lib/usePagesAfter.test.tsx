import { act, renderHook } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { ApiError } from '@/lib/api'
import { usePagesAfter } from '@/lib/usePagesAfter'

interface Page {
  items: string[]
  nextCursor: string | null
}

const rowsOf = (page: Page) => page.items

/** A promise this test settles by hand, so a page can arrive late. */
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

function setup(first: Page | undefined, fetchAfter: (cursor: string) => Promise<Page>) {
  return renderHook(({ first }) => usePagesAfter(first, fetchAfter, rowsOf), { initialProps: { first } })
}

describe('usePagesAfter', () => {
  it('shows nothing before there is a first page', () => {
    const { result } = setup(undefined, vi.fn())
    expect(result.current.rows).toEqual([])
    expect(result.current.nextCursor).toBeNull()
  })

  it('appends each page after the first, in order, following its cursor', async () => {
    const fetchAfter = vi.fn(async (cursor: string) =>
      cursor === 'c1' ? { items: ['c', 'd'], nextCursor: 'c2' } : { items: ['e'], nextCursor: null },
    )
    const { result } = setup({ items: ['a', 'b'], nextCursor: 'c1' }, fetchAfter)
    expect(result.current.rows).toEqual(['a', 'b'])
    expect(result.current.nextCursor).toBe('c1')

    await act(() => result.current.loadMore())
    expect(result.current.rows).toEqual(['a', 'b', 'c', 'd'])
    expect(result.current.nextCursor).toBe('c2')

    await act(() => result.current.loadMore())
    expect(fetchAfter.mock.calls.map(([cursor]) => cursor)).toEqual(['c1', 'c2'])
    expect(result.current.rows).toEqual(['a', 'b', 'c', 'd', 'e'])
    expect(result.current.nextCursor).toBeNull()
  })

  it('is loading while a page is on its way, and ignores a second click meanwhile', async () => {
    const page = deferred<Page>()
    const fetchAfter = vi.fn(() => page.promise)
    const { result } = setup({ items: ['a'], nextCursor: 'c1' }, fetchAfter)

    let pending!: Promise<void>
    act(() => {
      pending = result.current.loadMore()
    })
    expect(result.current.loading).toBe(true)
    await act(() => result.current.loadMore())
    expect(fetchAfter).toHaveBeenCalledTimes(1)

    await act(async () => {
      page.resolve({ items: ['b'], nextCursor: null })
      await pending
    })
    expect(result.current.loading).toBe(false)
    expect(result.current.rows).toEqual(['a', 'b'])
  })

  it('starts over when the first page is replaced', async () => {
    const fetchAfter = vi.fn(async () => ({ items: ['b'], nextCursor: null }))
    const { result, rerender } = setup({ items: ['a'], nextCursor: 'c1' }, fetchAfter)
    await act(() => result.current.loadMore())
    expect(result.current.rows).toEqual(['a', 'b'])

    rerender({ first: { items: ['x'], nextCursor: 'k1' } })
    expect(result.current.rows).toEqual(['x'])
    expect(result.current.nextCursor).toBe('k1')
  })

  it('drops a page that arrives after its first page was replaced', async () => {
    const late = deferred<Page>()
    const { result, rerender } = setup({ items: ['a'], nextCursor: 'c1' }, () => late.promise)

    let pending!: Promise<void>
    act(() => {
      pending = result.current.loadMore()
    })
    // A new search lands while the old search's page 2 is still on its way.
    rerender({ first: { items: ['x'], nextCursor: null } })
    expect(result.current.loading).toBe(false)

    await act(async () => {
      late.resolve({ items: ['stale'], nextCursor: 'c2' })
      await pending
    })
    expect(result.current.rows).toEqual(['x'])
    expect(result.current.nextCursor).toBeNull()
  })

  it('drops a failure that arrives after its first page was replaced', async () => {
    const late = deferred<Page>()
    const { result, rerender } = setup({ items: ['a'], nextCursor: 'c1' }, () => late.promise)

    let pending!: Promise<void>
    act(() => {
      pending = result.current.loadMore()
    })
    rerender({ first: { items: ['x'], nextCursor: 'k1' } })
    await act(async () => {
      late.reject(new ApiError('network_error', 'offline', true))
      await pending
    })
    expect(result.current.error).toBeNull()
    expect(result.current.rows).toEqual(['x'])
  })

  it('keeps the rows shown when a page fails, and the retry clears the error', async () => {
    const fetchAfter = vi
      .fn<(cursor: string) => Promise<Page>>()
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce({ items: ['b'], nextCursor: null })
    const { result } = setup({ items: ['a'], nextCursor: 'c1' }, fetchAfter)

    await act(() => result.current.loadMore())
    expect(result.current.rows).toEqual(['a'])
    expect(result.current.error?.code).toBe('unknown_error')
    expect(result.current.error?.message).toBe('boom')
    expect(result.current.nextCursor).toBe('c1')

    await act(() => result.current.loadMore())
    expect(fetchAfter.mock.calls.map(([cursor]) => cursor)).toEqual(['c1', 'c1'])
    expect(result.current.error).toBeNull()
    expect(result.current.rows).toEqual(['a', 'b'])
  })

  it('keeps an ApiError as it is', async () => {
    const err = new ApiError('network_error', 'offline', true)
    const { result } = setup({ items: ['a'], nextCursor: 'c1' }, () => Promise.reject(err))
    await act(() => result.current.loadMore())
    expect(result.current.error).toBe(err)
  })
})
