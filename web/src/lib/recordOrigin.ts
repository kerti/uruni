import type { Location } from 'react-router-dom'

/**
 * Catat opened from a screen other than its own nav button (#745): Status
 * iuran's Catat pembayaran, an amplop's Catat sumbangan, Talangan's Catat.
 * Saving and Kembali both go back to `returnTo` - that screen's path and
 * query, so the view and month she left are the ones she lands on - instead of
 * Beranda. Carried in the history entry's state, like every other one-entry
 * hand-off here; a reload keeps it, a fresh visit to /record has none.
 */
export interface RecordOriginState {
  returnTo: string
}

/** The state to open Catat with from `location`. */
export function recordOriginFrom(location: Pick<Location, 'pathname' | 'search'>): RecordOriginState {
  return { returnTo: location.pathname + location.search }
}

/** The screen to return to from Catat, if it was opened from one. Only an
 * in-app path is honoured: a value that is not one ("//host", a full URL)
 * falls back to Beranda. */
export function returnToOf(state: unknown): string | null {
  const returnTo = (state as Partial<RecordOriginState> | null)?.returnTo
  return typeof returnTo === 'string' && returnTo.startsWith('/') && !returnTo.startsWith('//') ? returnTo : null
}
