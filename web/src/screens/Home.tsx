import { useEffect } from 'react'

import ReconciliationBanner from '@/components/ReconciliationBanner'
import TransactionList from '@/components/TransactionList'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { Button } from '@/components/ui/button'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { getBalances } from '@/lib/balances'
import { formatUnixSeconds } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { getLatestReconciliation, listOpenReconciliationLines } from '@/lib/reconciliations'
import { listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { AccountBalance, Balances } from '@/lib/balances'
import type { LatestReconciliation, OpenReconciliationLine } from '@/lib/reconciliations'
import type { Transaction } from '@/lib/transactions'

/** How many of the most recent transactions the recent-activity list shows -
 * a client-side slice of GET /api/transactions's first page, which is
 * already newest-first (#225). */
const RECENT_ACTIVITY_COUNT = 5


interface HomeData {
  balances: Balances
  openLines: OpenReconciliationLine[]
  latest: LatestReconciliation | null
  transactions: Transaction[]
}

/**
 * The home screen (M6.9, PRD section 7.7): the everyday-loop landing page reached
 * at "/" once a fund exists (App.tsx's AuthedGate). Order per
 * Design-System.md:91 - balance hero, per-location rows, reconciliation
 * banner + last-checked, recent activity. Navigation is Shell's footer
 * (M6.15) and the post-record success banner is App.tsx's, not this
 * screen's - see those files' own comments. The reconciliation banner is
 * the one destination home still owns, per M6.10's ruling that the banner
 * IS reconcile's affordance.
 *
 * `refetchKey` changes whenever App.tsx's router state carries a fresh
 * "recorded" navigation (a successful POST /api/transactions just
 * happened), so a new entry is visible in recent activity without a manual
 * refresh, without this screen needing any router knowledge of its own.
 */
export default function Home({
  refetchKey,
  onReconcile,
  onViewReimbursements,
  onViewIncidentals,
  onViewHistory,
}: {
  refetchKey: unknown
  onReconcile: () => void
  onViewReimbursements: () => void
  onViewIncidentals: () => void
  /** Riwayat's Transaksi tab (M6.23) - the "lihat semua" link below. Same
   * caller-owns-navigation contract as the two callbacks above: this screen
   * stays router-agnostic and App.tsx supplies the actual navigate() call. */
  onViewHistory: () => void
}) {
  const [state, run] = useApi<HomeData>()

  async function loadHomeData(): Promise<HomeData> {
    const [balances, openLines, transactionsPage, latest] = await Promise.all([
      getBalances(),
      listOpenReconciliationLines(),
      listTransactions(),
      // A fresh fund has never been reconciled - GET
      // /api/reconciliations/latest answers 404 `not_found` for that, a
      // normal first-run state (reconciliations.go's latestReconciliation),
      // not a load failure. Any other error still fails the whole load,
      // same as the other three calls.
      getLatestReconciliation().catch((err: unknown) => {
        if (err instanceof ApiError && err.code === 'not_found') return null
        throw err
      }),
    ])
    return { balances, openLines, transactions: transactionsPage.transactions, latest }
  }

  useEffect(() => {
    void run(loadHomeData)
    // run is a stable useCallback (useApi.ts); refetchKey is the deliberate
    // extra dependency that makes a fresh "recorded" navigation reload this
    // screen's data.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run, refetchKey])

  // Re-read the fund whenever the app comes back to the foreground.
  //
  // On iOS an installed PWA is usually evicted while it is in the
  // background, so returning to it is a cold relaunch and this screen
  // remounts with fresh data anyway - but that is memory pressure, not a
  // guarantee. Switch back quickly, or resume on Android or a desktop, and
  // the app comes back warm: same React tree, same numbers, however old
  // they are. A stale balance looks exactly like a current one, which is
  // the one thing this screen must never do (PRD section 7.7).
  //
  // Silent, so an app switch never blanks the screen to "Memuat..." and a
  // moment without signal never replaces it with ErrorState - see
  // RunOptions.silent.
  useEffect(() => {
    function refreshWhenVisible() {
      if (document.visibilityState === 'visible') void run(loadHomeData, { silent: true })
    }
    document.addEventListener('visibilitychange', refreshWhenVisible)
    return () => document.removeEventListener('visibilitychange', refreshWhenVisible)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run])

  if (state.status === 'idle' || state.status === 'loading') {
    return <Loading />
  }

  if (state.status === 'error' || !state.data) {
    return state.error ? <ErrorState error={state.error} onRetry={() => void run(loadHomeData)} /> : null
  }

  const { balances, openLines, latest, transactions } = state.data
  // GET /api/transactions's first page is already newest-first (#225) - no
  // more reversing a client-side slice of an oldest-first list.
  const recentTransactions = transactions.slice(0, RECENT_ACTIVITY_COUNT)

  // A transaction row carries purpose_id, not a purpose name - and
  // GET /api/balances already answers with every purpose and account the
  // fund has, names included. So "what was this entry?" costs a lookup, not
  // a fifth request. A member name would need GET /api/members and is not
  // this slice's (M6.13 gives dues entries a member of their own).
  const purposeNames = new Map(balances.purposes.map((purpose) => [purpose.id, purpose.name]))

  return (
    <div className="flex flex-col gap-6">
      {/* "Balance is the hero" (Design-System.md:91). The prominence comes
          from surface, space and color - a card at the system's own radius
          and card elevation, 24px of padding, the figure in Forest - not
          from a bigger number: 36px/700 tabular IS the Balance/display role,
          the top of the type scale, and inventing a larger size would put
          this screen outside the system.
          Deliberately not a filled Forest block either. Forest (#1F5D50) and
          success (#2E7D5B) are neighbours, so a green slab sitting directly
          above a green "cocok" banner would read as one green mass and blur
          the reconciliation signal the design system calls its emotional
          heart.
          Filled with Forest (`--primary`) and white text, which passes AA -
          Design-System.md's own contrast note is why the fill is Forest and
          never Sage. The cost, accepted deliberately: Forest is also the
          action color, so this surface shares a hue with the footer's
          active tab. They do not compete - one is a filled block mid-screen,
          the other an 11px label pinned to the bottom edge.
          Not `--secondary` (#E7F1EA), the soft-sage highlight surface: it is
          a shade away from success-soft (#E3F1E9) and would collide with
          the reconciliation banner directly beneath it. */}
      <section
        aria-label={copy.home.balanceHeading}
        className="flex flex-col items-center gap-2 rounded-2xl bg-primary px-6 py-6 text-center text-primary-foreground shadow-card"
      >
        <p className="text-[13px] font-medium text-primary-foreground/80">{copy.home.balanceHeading}</p>
        <p className="tabular text-[36px] font-bold leading-tight">{formatIDR(balances.fund_total)}</p>
      </section>

      <section className="flex flex-col gap-2">
        <h2 className="text-sm font-semibold text-muted-foreground">{copy.home.locationsHeading}</h2>
        <ul className="flex flex-col gap-2">
          {balances.accounts.map((account: AccountBalance) => (
            <li key={account.id} className="flex items-center justify-between rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
              <span>{account.name}</span>
              <span className="tabular font-medium">{formatIDR(account.balance)}</span>
            </li>
          ))}
        </ul>
      </section>

      <section className="flex flex-col gap-2">
        {/* A 404 from /api/reconciliations/latest is the only signal that no
            count has ever been taken - open-lines answers [] either way, so
            the banner cannot tell "nothing open" from "never looked" on its
            own. */}
        {/* The reconcile screen's entry point (M6.10) - the banner itself is
            the natural affordance, per the orchestrator's own ruling, so
            there is no separate button anywhere else on this screen.
            onReconcile is App.tsx's navigate('/reconcile'), the same
            caller-owns-navigation contract RecordTransaction.tsx's
            onRecorded/onCancel already use - Home stays router-agnostic,
            same as every other screen in this app. */}
        <ReconciliationBanner openLines={openLines} everReconciled={latest !== null} onClick={onReconcile} />
        {latest && <p className="text-sm text-muted-foreground">{copy.home.lastChecked(formatUnixSeconds(latest.performed_at))}</p>}
      </section>

      <Button type="button" variant="outline" size="lg" className="w-full" onClick={onViewReimbursements}>
        {copy.home.reimbursementLink}
      </Button>

      <Button type="button" variant="outline" size="lg" className="w-full" onClick={onViewIncidentals}>
        {copy.home.incidentalLink}
      </Button>

      <section className="flex flex-col gap-2">
        <div className="flex items-center justify-between gap-3">
          <h2 className="text-sm font-semibold text-muted-foreground">{copy.home.recentActivityHeading}</h2>
          {/* min-h-11 overrides the `link` variant's own compact height
              (Design-System.md's 44px minimum touch target) - the same
              override the header's logout button already uses on `icon`. */}
          <Button type="button" variant="link" className="h-auto min-h-11 p-0" onClick={onViewHistory}>
            {copy.home.recentActivityViewAll}
          </Button>
        </div>
        <TransactionList transactions={recentTransactions} purposeNames={purposeNames} emptyMessage={copy.home.recentActivityEmpty} />
      </section>
    </div>
  )
}
