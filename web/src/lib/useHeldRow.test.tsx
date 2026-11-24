import { renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

import { useHeldRow } from '@/lib/useHeldRow'

interface Row {
  id: number
  name: string
}

function setup(id: number | null, rows: Row[]) {
  return renderHook(({ id, rows }) => useHeldRow(id, rows), { initialProps: { id, rows } })
}

describe('useHeldRow', () => {
  it('finds the row in the list', () => {
    const { result } = setup(2, [
      { id: 1, name: 'a' },
      { id: 2, name: 'b' },
    ])
    expect(result.current).toEqual({ id: 2, name: 'b' })
  })

  it('is null for an id the list never held', () => {
    const { result } = setup(9, [{ id: 1, name: 'a' }])
    expect(result.current).toBeNull()
  })

  it('is null with no id', () => {
    const { result } = setup(null, [{ id: 1, name: 'a' }])
    expect(result.current).toBeNull()
  })

  it('holds the row while the list starts over without it', () => {
    const { result, rerender } = setup(2, [
      { id: 1, name: 'a' },
      { id: 2, name: 'b' },
    ])
    rerender({ id: 2, rows: [{ id: 1, name: 'a' }] })
    expect(result.current).toEqual({ id: 2, name: 'b' })
  })

  it('takes the fresher copy when the list has the row again', () => {
    const { result, rerender } = setup(2, [{ id: 2, name: 'b' }])
    rerender({ id: 2, rows: [] })
    rerender({ id: 2, rows: [{ id: 2, name: 'b2' }] })
    expect(result.current).toEqual({ id: 2, name: 'b2' })
  })

  it('lets go when the id moves on', () => {
    const { result, rerender } = setup(2, [{ id: 2, name: 'b' }])
    rerender({ id: null, rows: [] })
    expect(result.current).toBeNull()
    rerender({ id: 3, rows: [] })
    expect(result.current).toBeNull()
    rerender({ id: 2, rows: [] })
    // Coming back to 2 (forward, after a delete) finds nothing to hold: the
    // screen strips the link rather than reopen a row the list no longer has.
    expect(result.current).toBeNull()
  })
})
