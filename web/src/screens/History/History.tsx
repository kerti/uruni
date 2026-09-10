import { NavLink, Outlet } from 'react-router-dom'

import { buttonVariants } from '@/components/ui/button'
import { copy } from '@/copy/id'

/** Riwayat's tabs, in the order ADR-032's second level lists them. Only the
 * first two ship in this slice - Iuran needed somewhere to go the moment it
 * lost its own footer slot, and Penggantian/Cek kas join once their own
 * screens exist. */
const tabs = [
  { to: 'transactions', label: copy.history.tabs.transactions },
  { to: 'dues', label: copy.history.tabs.dues },
] as const

/**
 * Riwayat's layout (M6.23, ADR-032): a tab strip over an `<Outlet/>`, one
 * level under Shell's footer. Tabs are routes - `/history/transactions` and
 * `/history/dues` - never component state, for the same reason Shell's own
 * footer already states: a deep link and the back button must both land on
 * the right tab. That is also why these are `NavLink`s styled like buttons
 * (via `buttonVariants`) rather than shadcn's `tabs` component, which would
 * bring its own `useState` and quietly break both.
 */
export default function History() {
  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-2xl font-semibold">{copy.shell.nav.history}</h1>
      <nav aria-label={copy.shell.nav.history} className="grid grid-cols-2 gap-2">
        {tabs.map(({ to, label }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) => buttonVariants({ variant: isActive ? 'default' : 'outline', size: 'lg', className: 'w-full' })}
          >
            {label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  )
}
