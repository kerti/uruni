import { useState } from 'react'

const unseen = Symbol('unseen')

/**
 * Runs `reset` whenever `key` differs from the key it last saw - and on the
 * first render, the moments a `useEffect(reset, [key])` would.
 *
 * The difference is when. An effect runs after the screen is drawn, so a
 * dialog re-seeding its fields from one can fire after the treasurer's first
 * keystroke and put the old value back under her typing. This runs during
 * render - React's pattern for adjusting state when a prop changes - so the
 * reset lands before anything reaches the screen.
 *
 * `reset` may only set state on the calling component. `key` is compared with
 * Object.is, so pass the thing whose change means "start over": typically
 * `open` for an add dialog, or `open ? row : null` for an edit dialog seeded
 * from a row.
 */
export function useResetWhen<T>(key: T, reset: () => void): void {
  const [seen, setSeen] = useState<T | typeof unseen>(unseen)
  if (!Object.is(seen, key)) {
    setSeen(key)
    reset()
  }
}
