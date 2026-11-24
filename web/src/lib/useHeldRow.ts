import { useState } from 'react'

/**
 * The row named `id` among `rows` - or, while `id` stays the same, the last
 * copy of it seen.
 *
 * A dialog over a listed row reads the row from the list. When the list
 * starts over (usePagesAfter: a re-read of the first page drops the pages
 * after it), a row from page two leaves `rows` while its dialog is still open
 * - typically showing the error from a write whose outcome is unknown. Held,
 * the dialog stays; a fresher copy replaces the held one whenever the list
 * has it again. An id never seen in the list (a stale deep link) stays null,
 * so the screen still strips it, and a held row is let go the moment `id`
 * changes - closing the dialog ends the hold.
 */
export function useHeldRow<R extends { id: number }>(id: number | null, rows: readonly R[]): R | null {
  const [held, setHeld] = useState<{ id: number | null; row: R | null }>({ id, row: null })
  const found = id === null ? null : (rows.find((row) => row.id === id) ?? null)
  // A new id lets go of the old row; a found row replaces the held copy.
  // Adjusting state during render, as useResetWhen does, so no stale frame is drawn.
  if (held.id !== id || (found !== null && found !== held.row)) setHeld({ id, row: found })
  if (found !== null) return found
  return held.id === id ? held.row : null
}
