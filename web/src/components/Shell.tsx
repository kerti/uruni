import { useEffect, type ReactNode } from 'react'
import { LogOut } from 'lucide-react'

import { Button } from '@/components/ui/button'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { logout } from '@/lib/auth'
import { useApi } from '@/lib/useApi'

/**
 * The frame every authenticated screen renders inside, from M6.8 onward
 * (M6.6). Three jobs, and deliberately no more:
 *
 * 1. A header naming the fund, so the treasurer always knows which kas she
 *    is looking at, with the app's only logout affordance next to it - M6.4
 *    wired POST /api/logout but left it unexposed, waiting for exactly this.
 * 2. Safe-area padding, so the header clears a notch and the bottom-anchored
 *    action clears a home indicator. index.html carries the matching
 *    `viewport-fit=cover`; without it the env() insets are all zero.
 * 3. A bottom-anchored slot for the primary action - Design-System.md's
 *    "primary action bottom-anchored; a circular add-FAB for catat
 *    transaksi". M6.8 is what finally passes something into it; nothing
 *    renders there until then.
 *
 * Not a nav bar: Uruni has no tab bar in the PRD, and adding one here would
 * be inventing navigation ahead of the screens that would need it.
 */
export default function Shell({
  title,
  onLoggedOut,
  action,
  children,
}: {
  /** Shown as the header's heading - the fund's name (GET /api/fund). */
  title: string
  /** Called once POST /api/logout has succeeded, so App can re-render Login. */
  onLoggedOut: () => void
  /** The bottom-anchored primary action, e.g. M6.8's add-FAB. Optional. */
  action?: ReactNode
  children: ReactNode
}) {
  const [state, run] = useApi<void>()

  useEffect(() => {
    if (state.status === 'success') onLoggedOut()
  }, [state.status, onLoggedOut])

  const loggingOut = state.status === 'loading'

  return (
    <div className="flex min-h-dvh flex-col bg-background">
      <header className="sticky top-0 z-10 border-b border-border bg-background/95 pt-[env(safe-area-inset-top)] backdrop-blur">
        <div className="flex items-center justify-between gap-3 py-2 pl-[max(1rem,env(safe-area-inset-left))] pr-[max(1rem,env(safe-area-inset-right))]">
          <h1 className="truncate text-lg font-semibold">{title}</h1>
          {/* size-11 (44px), not the `icon` variant's 32px: Design-System.md
              sets a 44x44 minimum touch target and this is the header's only
              control. The label is on the button, not a tooltip - a tooltip
              is invisible to a thumb. */}
          <Button
            variant="ghost"
            size="icon"
            className="size-11"
            onClick={() => void run(logout)}
            disabled={loggingOut}
            aria-label={loggingOut ? copy.shell.loggingOut : copy.shell.logout}
          >
            <LogOut aria-hidden="true" className="size-5" />
          </Button>
        </div>
      </header>

      <main
        className={`flex-1 py-4 pl-[max(1rem,env(safe-area-inset-left))] pr-[max(1rem,env(safe-area-inset-right))] ${
          // Only reserve room for the floating action when there is one -
          // otherwise every screen carries a dead 6rem gutter.
          action ? 'pb-[calc(env(safe-area-inset-bottom)+6rem)]' : 'pb-[calc(env(safe-area-inset-bottom)+1rem)]'
        }`}
      >
        {state.status === 'error' && state.error && (
          <div className="mb-4">
            <ErrorState error={state.error} onRetry={() => void run(logout)} />
          </div>
        )}
        {children}
      </main>

      {action && (
        <div className="pointer-events-none fixed inset-x-0 bottom-0 z-20 flex justify-end px-[max(1rem,env(safe-area-inset-right))] pb-[calc(env(safe-area-inset-bottom)+1rem)]">
          <div className="pointer-events-auto">{action}</div>
        </div>
      )}
    </div>
  )
}
