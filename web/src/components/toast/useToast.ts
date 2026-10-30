import { createContext, useContext } from 'react'

/** How long a success stays up. Long enough to read one sentence, short
 * enough to be gone before the next thing she does. */
export const SUCCESS_TOAST_MS = 4000

export interface Toasts {
  /** A save went through. Hides itself after SUCCESS_TOAST_MS; a tap hides it sooner. */
  success: (text: string) => void
  /** Something is saved but part of it did not go through (a photo, say). Stays until tapped: never alarm-red, never timed. */
  attention: (text: string) => void
}

// With no provider mounted (a screen rendered alone in a test) a toast has
// nowhere to go and is dropped; the app mounts one in AuthGate.
const noToasts: Toasts = { success: () => {}, attention: () => {} }

export const ToastContext = createContext<Toasts>(noToasts)

/** The one way to say "saved" (#743). */
export function useToast(): Toasts {
  return useContext(ToastContext)
}
