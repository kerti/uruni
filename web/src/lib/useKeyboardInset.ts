import { useSyncExternalStore } from 'react'

/** How much of the bottom of the layout viewport the on-screen keyboard is
 * covering, and how tall the part she can still see is - both in CSS px. */
export interface KeyboardInset {
  inset: number
  height: number
}

const NO_KEYBOARD: KeyboardInset = { inset: 0, height: 0 }

// useSyncExternalStore compares snapshots by identity, so the object is
// only replaced when a value actually changes.
let cachedKey = ''
let cached: KeyboardInset = NO_KEYBOARD

function subscribe(onChange: () => void): () => void {
  const viewport = window.visualViewport
  if (!viewport) return () => {}
  viewport.addEventListener('resize', onChange)
  viewport.addEventListener('scroll', onChange)
  return () => {
    viewport.removeEventListener('resize', onChange)
    viewport.removeEventListener('scroll', onChange)
  }
}

function getSnapshot(): KeyboardInset {
  const viewport = window.visualViewport
  if (!viewport) return NO_KEYBOARD
  const inset = Math.max(0, Math.round(window.innerHeight - viewport.height - viewport.offsetTop))
  const height = Math.round(viewport.height)
  const key = `${inset}:${height}`
  if (key !== cachedKey) {
    cachedKey = key
    cached = { inset, height }
  }
  return cached
}

function getServerSnapshot(): KeyboardInset {
  return NO_KEYBOARD
}

/**
 * The on-screen keyboard, measured through `window.visualViewport`.
 *
 * iOS Safari - and Chrome on Android since 108, whose default is
 * `interactive-widget=resizes-visual` - does not shrink the layout viewport
 * when the keyboard opens; it slides the keyboard over the page. A
 * `position: fixed` dialog is still placed against the layout viewport, part
 * of which is now behind the keyboard, and `dvh` does not help because the
 * layout viewport never changed. Only the visual viewport shrinks, so that
 * is what gets measured: the gap between its bottom edge and the layout
 * viewport's, and how tall the part still showing is.
 *
 * Browsers without `visualViewport` (and jsdom) read as no keyboard.
 */
export function useKeyboardInset(): KeyboardInset {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}
