import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { CircleAlert, CircleCheck } from 'lucide-react'

import { SUCCESS_TOAST_MS, ToastContext, type Toasts } from '@/components/toast/useToast'
import { cn } from '@/lib/utils'

type Tone = 'success' | 'attention'

interface Toast {
  /** A new id per call, so the same sentence twice in a row is still a new toast (re-announced, timer restarted). */
  id: number
  tone: Tone
  text: string
}

/**
 * The app's single confirmation surface: one toast at a time, under the
 * header and inside the 576px column, replaced by the next. The live region
 * is always mounted, even when empty, because a screen reader only announces
 * text inserted into a region that already exists. The whole toast is the
 * dismiss button (a 44px-tall target whose name is the message itself).
 */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const nextId = useRef(0)

  const dismiss = useCallback(() => {
    clearTimeout(timer.current)
    setToast(null)
  }, [])

  const show = useCallback((tone: Tone, text: string) => {
    clearTimeout(timer.current)
    nextId.current += 1
    setToast({ id: nextId.current, tone, text })
    if (tone === 'success') {
      timer.current = setTimeout(() => setToast(null), SUCCESS_TOAST_MS)
    }
  }, [])

  useEffect(() => () => clearTimeout(timer.current), [])

  const toasts = useMemo<Toasts>(() => ({ success: (text) => show('success', text), attention: (text) => show('attention', text) }), [show])

  return (
    <ToastContext.Provider value={toasts}>
      {children}
      <div
        role="status"
        aria-live="polite"
        aria-atomic="true"
        className="pointer-events-none fixed inset-x-0 top-[calc(env(safe-area-inset-top)+4.25rem)] z-30"
      >
        <div className="mx-auto w-full max-w-xl pl-[max(1rem,env(safe-area-inset-left))] pr-[max(1rem,env(safe-area-inset-right))]">
          {toast && (
            <button
              key={toast.id}
              type="button"
              onClick={dismiss}
              // Liquid glass (#743): a translucent tint over the blurred,
              // slightly saturated page, a bright top edge and a soft drop
              // shadow, so it floats above everything and never reads as the
              // cek kas card. Success is 75% so the page shows through; the
              // warning is 92% because it asks her to act and must read clearly
              // over anything, the dark balance card included.
              className={cn(
                'pointer-events-auto flex min-h-11 w-full animate-reveal items-center gap-2 rounded-lg px-3 py-2 text-left text-sm shadow-[inset_0_1px_0_rgb(255_255_255/0.7),inset_0_0_0_1px_rgb(255_255_255/0.35),0_10px_25px_-8px_rgb(0_0_0/0.25)] backdrop-blur-[14px] backdrop-saturate-[1.7] outline-none focus-visible:ring-3',
                toast.tone === 'success'
                  ? 'bg-success-soft/75 text-success focus-visible:ring-success/40'
                  : 'bg-attention-soft/92 text-attention-text focus-visible:ring-attention/40',
              )}
            >
              {toast.tone === 'success' ? (
                <CircleCheck aria-hidden="true" className="size-5 shrink-0" />
              ) : (
                <CircleAlert aria-hidden="true" className="size-5 shrink-0" />
              )}
              <span>{toast.text}</span>
            </button>
          )}
        </div>
      </div>
    </ToastContext.Provider>
  )
}
