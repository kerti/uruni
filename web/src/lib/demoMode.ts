import { createContext, useContext } from 'react'

/**
 * Whether this is the public demo (ADR-045), for the screens that change under
 * it: the two Akun bendahara dialogs, whose save is disabled there. App provides
 * it from the session probe, which carries the demo login only under
 * URUNI_DEMO; everywhere else it is false and nothing changes.
 */
export const DemoMode = createContext(false)

export function useDemoMode(): boolean {
  return useContext(DemoMode)
}
