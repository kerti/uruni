import { useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'

/** The private tag this hook writes to `location.state` on the entry `open()`
 * pushes - a marker `close()` reads back to tell "this entry exists because a
 * dialog was opened from within the app" from "the browser landed here
 * directly" (a deep link, a reload, a bookmark). */
interface DialogLocationState {
  dialog?: boolean
  /** Set by `clear()`: this entry differs from the one before it only by a
   * dialog param removed in place - see useRefetchKey. */
  dialogCleared?: boolean
}

/** Whether this history entry exists only because a dialog opened or
 * closed in place - nothing about the screen's data changed. */
function isDialogEntry(state: unknown): boolean {
  const s = state as DialogLocationState | null
  return s?.dialog === true || s?.dialogCleared === true
}

/**
 * The "reload your data" signal App.tsx hands every screen that refetches on
 * navigation: `location.key`, except across entries a dialog pushed or
 * cleared.
 *
 * Every navigation mints a new `location.key`, and an open dialog is a
 * navigation (ADR-032). Fed straight to a screen's refetch, opening a dialog
 * reloaded the list underneath it - dropping it to Loading, discarding the
 * pages loaded with "Muat lebih banyak". Opening and closing a dialog changes
 * nothing a list shows (a write inside one refreshes its own screen
 * explicitly), so the key only moves on an entry that is not a dialog's, and
 * going back from a dialog lands on the very entry - same key - it was opened
 * from.
 */
export function useRefetchKey(): string {
  const location = useLocation()
  const [key, setKey] = useState(location.key)
  // Derived state, set during render (React's pattern for it) so the screen
  // never renders once with a stale key and once with the new one.
  if (!isDialogEntry(location.state) && location.key !== key) {
    setKey(location.key)
  }
  return isDialogEntry(location.state) ? key : location.key
}

/**
 * Search-param-driven dialog state (ADR-032 "An open dialog is a search
 * parameter"). No component holds modal open/closed state: the URL is the
 * source of truth, so back/forward, reload and a deep link all agree with
 * what is on screen.
 *
 * `open(value)` pushes a history entry carrying `?<param>=<value>` and marks
 * it with the tag above. `close()` reads the tag back: when the current entry
 * was pushed by `open`, closing is `navigate(-1)`, so Batal, a backdrop tap,
 * Esc and the back gesture all land in the same place - never a fresh entry
 * stacked behind the one that opened it. When it was NOT pushed by `open` (a
 * deep link straight to `?edit=...`), `close()` strips the param in place
 * (`replace: true`) instead of navigating the user out of the app.
 */
export function useDialogParam(param = 'edit') {
  const [searchParams, setSearchParams] = useSearchParams()
  const location = useLocation()
  const navigate = useNavigate()

  const value = searchParams.get(param)

  // react-router hands back a new setSearchParams whenever the search
  // changes. Reading it through a ref ties open/clear/close identities to
  // `param` alone, so an effect that calls clear() can list it without
  // re-running on every URL change.
  const setSearchParamsRef = useRef(setSearchParams)
  useEffect(() => {
    setSearchParamsRef.current = setSearchParams
  })

  const open = useCallback(
    (next: string) => {
      setSearchParamsRef.current(
        (prev) => {
          const params = new URLSearchParams(prev)
          params.set(param, next)
          return params
        },
        { state: { dialog: true } satisfies DialogLocationState },
      )
    },
    [param],
  )

  // Strips the param in place and never moves through history. close()'s
  // fallback, and the right call for a param naming something that does not
  // exist: close() there can run after a close() already in flight has gone
  // back, and a second navigate(-1) takes her off the screen entirely.
  const clear = useCallback(() => {
    setSearchParamsRef.current(
      (prev) => {
        const params = new URLSearchParams(prev)
        params.delete(param)
        return params
      },
      { replace: true, state: { dialogCleared: true } satisfies DialogLocationState },
    )
  }, [param])

  const close = useCallback(() => {
    const state = location.state as DialogLocationState | null
    if (state?.dialog === true) {
      navigate(-1)
      return
    }
    clear()
  }, [clear, location.state, navigate])

  return { value, open, close, clear }
}
