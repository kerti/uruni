import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import { useKeyboardInset } from '@/lib/useKeyboardInset'

/** jsdom has no visualViewport - a stand-in with the three things the hook
 * reads, and real events so the subscription is exercised too. */
class FakeViewport extends EventTarget {
  height = 800
  offsetTop = 0
}

let viewport: FakeViewport

beforeEach(() => {
  viewport = new FakeViewport()
  Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
  Object.defineProperty(window, 'visualViewport', { configurable: true, value: viewport })
})

afterEach(() => {
  Reflect.deleteProperty(window, 'visualViewport')
  Reflect.deleteProperty(window, 'innerHeight')
})

function resize(height: number, offsetTop = 0) {
  act(() => {
    viewport.height = height
    viewport.offsetTop = offsetTop
    viewport.dispatchEvent(new Event('resize'))
  })
}

describe('useKeyboardInset', () => {
  it('reads no inset while the keyboard is closed', () => {
    const { result } = renderHook(() => useKeyboardInset())
    expect(result.current).toEqual({ inset: 0, height: 800 })
  })

  it('reports how far the keyboard covers the bottom once it opens, and clears when it closes', () => {
    const { result } = renderHook(() => useKeyboardInset())

    resize(500)
    expect(result.current).toEqual({ inset: 300, height: 500 })

    resize(800)
    expect(result.current).toEqual({ inset: 0, height: 800 })
  })

  it('subtracts a scrolled visual viewport, which iOS does to reveal a focused field', () => {
    const { result } = renderHook(() => useKeyboardInset())

    resize(500, 100)
    expect(result.current).toEqual({ inset: 200, height: 500 })
  })

  it('reads as no keyboard where visualViewport does not exist', () => {
    Reflect.deleteProperty(window, 'visualViewport')
    const { result } = renderHook(() => useKeyboardInset())
    expect(result.current).toEqual({ inset: 0, height: 0 })
  })
})
