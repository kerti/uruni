import { useEffect, useRef } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'

import { useToast } from '@/components/toast/useToast'

export interface ArrivalToast {
  tone: 'success' | 'attention'
  text: string
}

/**
 * A screen that finishes a save by navigating hands its confirmation to the
 * next screen in the history entry's state. This fires that confirmation as a
 * toast once, then clears the state with a replace, so a reload or Back does
 * not toast it again. `pick` reads the state and must be a stable function
 * (module level).
 *
 * The replace carries `dialogCleared`, the tag useRefetchKey already reads as
 * "same screen, only the entry's state changed": clearing the message must not
 * make the screen reload its list a second time.
 */
export function useArrivalToast(pick: (state: unknown) => ArrivalToast | null): void {
  const location = useLocation()
  const navigate = useNavigate()
  const toast = useToast()
  // StrictMode runs effects twice before the replace has landed; one entry
  // fires once.
  const fired = useRef<string | null>(null)

  useEffect(() => {
    const arrival = pick(location.state)
    if (!arrival || fired.current === location.key) return
    fired.current = location.key
    toast[arrival.tone](arrival.text)
    navigate(
      { pathname: location.pathname, search: location.search, hash: location.hash },
      { replace: true, state: { dialogCleared: true } },
    )
  }, [location, navigate, pick, toast])
}
