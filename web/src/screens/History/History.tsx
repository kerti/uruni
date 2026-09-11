import { NavLink, Outlet } from 'react-router-dom'

import { buttonVariants } from '@/components/ui/button'
import { copy } from '@/copy/id'

/** Riwayat's tabs, in the order ADR-032's second level lists them. Cek kas's
 * label reuses copy.reconciliation.heading rather than a fourth key under
 * copy.history.tabs, so the word "Cek kas" has exactly one source (#227). */
const tabs = [
  { to: 'transactions', label: copy.history.tabs.transactions },
  { to: 'dues', label: copy.history.tabs.dues },
  { to: 'reimbursements', label: copy.history.tabs.reimbursements },
  { to: 'reconciliations', label: copy.reconciliation.heading },
] as const

/**
 * Riwayat's layout (M6.23, ADR-032): a tab strip over an `<Outlet/>`, one
 * level under Shell's footer. Tabs are routes - `/history/transactions`,
 * `/history/dues`, `/history/reimbursements`, `/history/reconciliations` -
 * never component state, for the same reason Shell's own footer already
 * states: a deep link and the back button must both land on the right tab.
 * That is also why these are `NavLink`s styled like buttons (via
 * `buttonVariants`) rather than shadcn's `tabs` component, which would
 * bring its own `useState` and quietly break both.
 */
export default function History() {
  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-2xl font-semibold">{copy.shell.nav.history}</h1>
      {/* A segmented control, not a row of separate buttons: one bordered
          track, the active tab filled Forest, the rest ghost - the shape
          itself says "exactly one of these". Each tab keeps lg's h-11 (44px
          touch target); text-sm and px-1 are what let four labels ("Cek
          kas" among them, #227) fit a quarter of a 375px screen each
          without overflowing. */}
      <nav aria-label={copy.shell.nav.history} className="grid grid-cols-4 gap-1 rounded-xl border border-border bg-muted p-1">
        {tabs.map(({ to, label }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              buttonVariants({
                variant: isActive ? 'default' : 'ghost',
                size: 'lg',
                className: `w-full min-w-0 px-1 text-sm ${isActive ? '' : 'text-muted-foreground'}`,
              })
            }
          >
            {label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  )
}
