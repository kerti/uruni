import { useCallback } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'

/** The private tag this hook writes to `location.state` on the entry it
 * pushes for `open()` - not app data, just a marker `close()` reads back to
 * tell "this entry exists because a dialog was opened from within the app"
 * apart from "the browser landed here directly" (a deep link, a reload, a
 * bookmark). */
interface DialogLocationState {
  dialog?: boolean
}

/**
 * Search-param-driven dialog state (ADR-032 "An open dialog is a search
 * parameter"). No component holds modal open/closed state of its own - the
 * URL is the source of truth, so back/forward, reload and a deep link all
 * agree with what is on screen.
 *
 * `open(value)` pushes a history entry carrying `?<param>=<value>` and
 * marks it with the tag above. `close()` reads the tag back: when the
 * current entry was pushed by `open`, closing is `navigate(-1)`, so Batal,
 * a backdrop tap, Esc and the browser/Android back gesture all land in
 * exactly the same place - never a fresh entry stacked behind the one that
 * opened it. When the current entry was NOT pushed by `open` (a deep link
 * straight to `?edit=...`), there is nothing inside the app to go back to,
 * so `close()` strips the param in place instead (`replace: true`) - never
 * navigating the user out of the app.
 */
export function useDialogParam(param = 'edit') {
  const [searchParams, setSearchParams] = useSearchParams()
  const location = useLocation()
  const navigate = useNavigate()

  const value = searchParams.get(param)

  const open = useCallback(
    (next: string) => {
      setSearchParams(
        (prev) => {
          const params = new URLSearchParams(prev)
          params.set(param, next)
          return params
        },
        { state: { dialog: true } satisfies DialogLocationState },
      )
    },
    [param, setSearchParams],
  )

  // Strips the param in place and never moves through history. close()'s
  // fallback, and on its own the right call for a param naming something
  // that does not exist: calling close() there instead can run after a
  // close() already in flight has gone back, and a second navigate(-1)
  // takes her off the screen entirely.
  const clear = useCallback(() => {
    setSearchParams(
      (prev) => {
        const params = new URLSearchParams(prev)
        params.delete(param)
        return params
      },
      { replace: true },
    )
  }, [param, setSearchParams])

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
