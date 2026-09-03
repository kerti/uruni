import { useEffect, type ReactNode } from 'react'
import { CalendarCheck, CirclePlus, Home, LogOut, Settings, Users } from 'lucide-react'
import { Link, NavLink } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { logout } from '@/lib/auth'
import { useApi } from '@/lib/useApi'

/** The footer's four destinations, in order. Kept here rather than passed
 * in: the bar is the app's navigation, and a caller that could vary it
 * would be a caller that could give one screen a different app. */
const navItems = [
  { to: '/', icon: Home, label: copy.shell.nav.home, end: true },
  { to: '/record', icon: CirclePlus, label: copy.shell.nav.record, end: false },
  // Iuran is a monthly obligation, not a pile of coins: Design-System.md
  // rules out money glyphs as imagery, and Users now belongs to Anggota.
  { to: '/dues', icon: CalendarCheck, label: copy.shell.nav.dues, end: false },
  { to: '/members', icon: Users, label: copy.shell.nav.members, end: false },
  { to: '/settings', icon: Settings, label: copy.shell.nav.settings, end: false },
] as const

/**
 * The frame every authenticated screen renders inside, from M6.8 onward
 * (M6.6). Three jobs, and deliberately no more:
 *
 * 1. A header naming the fund, so the treasurer always knows which kas she
 *    is looking at, with the app's only logout affordance next to it - M6.4
 *    wired POST /api/logout but left it unexposed, waiting for exactly this.
 * 2. Safe-area padding, so the header clears a notch and the footer clears a
 *    home indicator. index.html carries the matching `viewport-fit=cover`;
 *    without it the env() insets are all zero.
 * 3. A sticky footer carrying the app's five destinations - home, record,
 *    dues, members, settings. Five is the platform's own cap for a tab bar,
 *    not over it: at 375px that is 75px a tab, which fits an 11px label.
 *
 * That footer replaces two earlier shapes, both deliberately (M6.15, the
 * maintainer's call). It replaces this file's own "not a nav bar" ruling,
 * which held while there were two screens and stopped holding at six; and
 * it replaces Design-System.md's circular add-FAB, since "Catat" is now a
 * tab and a screen may not have two entry points. The quiet links home
 * carried to dues and settings go with it, for the same reason.
 *
 * Reconcile is deliberately not a tab: M6.10's ruling is that the
 * reconciliation banner on home IS its affordance, and a tab would give that
 * screen a second door.
 *
 * The header's fund name is a link home as well (M6.16). That is a second
 * way to the same place, never the only one - a top-corner control is the
 * hardest thing on a phone to reach one-handed, which is exactly why the
 * Beranda tab stays.
 */
export default function Shell({
  title,
  onLoggedOut,
  children,
}: {
  /** Shown as the header's heading - the fund's name (GET /api/fund). */
  title: string
  /** Called once POST /api/logout has succeeded, so App can re-render Login. */
  onLoggedOut: () => void
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
          <h1 className="min-w-0 truncate text-lg font-semibold">
            {/* No aria-label: one here would become the *heading's*
                accessible name too, so the h1 would announce as "Kas RT 04 -
                kembali ke beranda" rather than the fund's name. A title link
                named after the site is the pattern a screen reader already
                knows. */}
            <Link to="/" className="block truncate rounded-lg outline-none focus-visible:ring-3 focus-visible:ring-ring/50">
              {title}
            </Link>
          </h1>
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

      {/* The footer is fixed, so main reserves its height (4rem) plus the
          home-indicator inset - otherwise the last row of every screen sits
          under the bar. */}
      <main className="flex-1 py-4 pb-[calc(env(safe-area-inset-bottom)+5rem)] pl-[max(1rem,env(safe-area-inset-left))] pr-[max(1rem,env(safe-area-inset-right))]">
        {state.status === 'error' && state.error && (
          <div className="mb-4">
            <ErrorState error={state.error} onRetry={() => void run(logout)} />
          </div>
        )}
        {children}
      </main>

      <nav
        aria-label={copy.shell.nav.label}
        className="fixed inset-x-0 bottom-0 z-20 border-t border-border bg-background/95 pb-[env(safe-area-inset-bottom)] backdrop-blur"
      >
        <ul className="flex items-stretch">
          {navItems.map(({ to, icon: Icon, label, end }) => (
            <li key={to} className="flex-1">
              {/* NavLink, not a Button: these are destinations, and the
                  active state has to come from the URL rather than from
                  anything Shell remembers - a deep link and a back button
                  must both land on the right tab. `end` on "/" only, so
                  home does not read as active on every other route. */}
              <NavLink
                to={to}
                end={end}
                className={({ isActive }) =>
                  `flex min-h-14 flex-col items-center justify-center gap-1 px-1 py-2 text-[11px] font-medium ${
                    // Forest for the current tab, muted for the rest -
                    // color plus aria-current, never color alone.
                    isActive ? 'text-primary' : 'text-muted-foreground'
                  }`
                }
              >
                {({ isActive }) => (
                  <>
                    <Icon aria-hidden="true" className={isActive ? 'size-6' : 'size-5'} />
                    <span>{label}</span>
                  </>
                )}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
    </div>
  )
}
