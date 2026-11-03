import { useEffect } from 'react'

import OpenIncidentalDialog from '@/components/OpenIncidentalDialog'
import ReconciliationBanner from '@/components/ReconciliationBanner'
import SectionDivider from '@/components/SectionDivider'
import TransactionList from '@/components/TransactionList'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { Button } from '@/components/ui/button'
import SectionLabel from '@/components/SectionLabel'
import { copy } from '@/copy/id'
import { errorCode } from '@/lib/api'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { getBalances } from '@/lib/balances'
import { formatUnixSeconds } from '@/lib/dates'
import { listIncidentals } from '@/lib/incidentals'
import { formatIDR } from '@/lib/money'
import { getLatestReconciliation, listOpenReconciliationLines } from '@/lib/reconciliations'
import { listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import type { AccountBalance, Balances, PurposeBalance } from '@/lib/balances'
import type { IncidentalListItem } from '@/lib/incidentals'
import type { LatestReconciliation, OpenReconciliationLine } from '@/lib/reconciliations'
import type { Transaction } from '@/lib/transactions'
import { card, tappableCard } from '@/components/cardClasses'
import RowChevron from '@/components/RowChevron'

/** How many of the most recent transactions the recent-activity list shows -
 * a client-side slice of GET /api/transactions's first page, which is already
 * newest-first. */
const RECENT_ACTIVITY_COUNT = 5

/** A Saldo per pos row's box. */
const rowClass = `flex items-center justify-between gap-3 ${tappableCard}`

interface HomeData {
  balances: Balances
  // Every envelope (GET /api/incidentals): which are still open - the piece
  // GET /api/balances does not carry, since closed_on lives on the incidental
  // row - each open one's participation line, and how many are closed.
  incidentals: IncidentalListItem[]
  openLines: OpenReconciliationLine[]
  latest: LatestReconciliation | null
  transactions: Transaction[]
}

/**
 * The home screen (PRD section 7.7): the everyday-loop landing page reached
 * at "/" once a fund exists (App.tsx's AuthedGate). Order per ADR-032 -
 * balance hero, per-location rows, reconciliation banner + last-checked,
 * purpose breakdown, recent activity. The purpose breakdown is also where an
 * envelope is opened and the closed ones are found (ADR-047), so this screen
 * hosts `?edit=incidental:new`. Navigation is Shell's footer and the
 * post-record success banner is App.tsx's, not this screen's. The
 * reconciliation banner is the one destination home owns: it IS reconcile's
 * affordance.
 *
 * `refetchKey` changes whenever App.tsx's router state carries a fresh
 * "recorded" navigation, so a new entry is visible in recent activity without
 * a manual refresh and without this screen needing router knowledge.
 */
export default function Home({
  refetchKey,
  onReconcile,
  onOpenIncidental,
  onViewTransactionsFor,
  onViewTransactionsForAccount,
  onViewHistory,
  onViewOwed,
  onIncidentalOpened,
  onViewClosedIncidentals,
}: {
  refetchKey: unknown
  onReconcile: () => void
  /** A tap on one of the purpose-breakdown's incidental rows - carries that
   * envelope's purpose id, mirroring the record form's `onRecordFor`
   * (Incidentals.tsx). Kas Utama and Titipan rows have no envelope to open, so
   * they call onViewTransactionsFor instead. Router-agnostic: the caller owns
   * navigation, as for the callbacks below. */
  onOpenIncidental: (purposeId: number) => void
  /** A tap on the Kas Utama or a Titipan row: Riwayat's Transaksi tab
   * filtered to that purpose (`?purpose=<id>`). */
  onViewTransactionsFor: (purposeId: number) => void
  /** A tap on a location row (#784): Riwayat's Transaksi tab filtered to
   * that location (`?account=<id>`). */
  onViewTransactionsForAccount: (accountId: number) => void
  /** Riwayat's Transaksi tab - the "lihat semua" link below. App.tsx supplies
   * the actual navigate() call. */
  onViewHistory: () => void
  /** The owed-to-members row: Riwayat's Talangan tab, outstanding view. */
  onViewOwed: () => void
  /** Buka amplop succeeded: the caller lands her on the new envelope. */
  onIncidentalOpened: (purposeId: number) => void
  /** The "Amplop yang sudah ditutup" row. */
  onViewClosedIncidentals: () => void
}) {
  const [state, run] = useApi<HomeData>()
  const { value: dialogValue, open: openDialog, close: closeDialog, clear: clearDialog } = useDialogParam()
  // This screen owns `incidental:new` only; an `incidental:<id>` that arrives
  // by hand names no dialog here (dialogTarget.ts).
  const dialogTarget = parseDialogTarget('incidental', dialogValue).kind
  const loaded = state.status === 'success'

  // A dead `incidental:` value is stripped once the screen has loaded, rather
  // than flash an empty dialog - clear(), never close(): it is not a
  // navigation to undo.
  useEffect(() => {
    if (dialogTarget === 'edit' || dialogTarget === 'invalid') {
      if (loaded) clearDialog()
    }
  }, [dialogTarget, loaded, clearDialog])

  async function loadHomeData(): Promise<HomeData> {
    const [balances, incidentals, openLines, transactionsPage, latest] = await Promise.all([
      getBalances(),
      listIncidentals(false),
      listOpenReconciliationLines(),
      listTransactions(),
      // A fresh fund has never been reconciled - GET
      // /api/reconciliations/latest answers 404 `not_found` for that, a normal
      // first-run state, not a load failure. Any other error still fails the
      // whole load, as for the other three calls.
      getLatestReconciliation().catch((err: unknown) => {
        if (errorCode(err) === 'not_found') return null
        throw err
      }),
    ])
    return { balances, incidentals, openLines, transactions: transactionsPage.transactions, latest }
  }

  useEffect(() => {
    void run(loadHomeData)
    // run is a stable useCallback (useApi.ts); refetchKey is the deliberate
    // extra dependency that makes a fresh "recorded" navigation reload.
  }, [run, refetchKey])

  // Re-read the fund whenever the app comes back to the foreground.
  //
  // On iOS an installed PWA is usually evicted while in the background, so
  // returning is a cold relaunch and this screen remounts with fresh data
  // anyway - but that is memory pressure, not a guarantee. Switch back
  // quickly, or resume on Android or a desktop, and the app comes back warm:
  // same React tree, same numbers, however old. A stale balance looks exactly
  // like a current one, which is the one thing this screen must never do
  // (PRD section 7.7).
  //
  // Silent, so an app switch never blanks the screen to "Memuat..." and a
  // moment without signal never replaces it with ErrorState (RunOptions.silent).
  useEffect(() => {
    function refreshWhenVisible() {
      if (document.visibilityState === 'visible') void run(loadHomeData, { silent: true })
    }
    document.addEventListener('visibilitychange', refreshWhenVisible)
    return () => document.removeEventListener('visibilitychange', refreshWhenVisible)
  }, [run])

  if (state.status === 'idle' || state.status === 'loading') {
    return <Loading />
  }

  if (state.status === 'error' || !state.data) {
    return state.error ? <ErrorState error={state.error} onRetry={() => void run(loadHomeData)} /> : null
  }

  const { balances, incidentals, openLines, latest, transactions } = state.data
  // GET /api/transactions's first page is already newest-first.
  const recentTransactions = transactions.slice(0, RECENT_ACTIVITY_COUNT)

  // A transaction row carries purpose_id, not a name - and GET /api/balances
  // answers with every purpose and account the fund has, names included, so
  // "what was this entry?" costs a lookup, not another request. A member's
  // name already rides on the row (member_name).
  const purposeNames = new Map(balances.purposes.map((purpose) => [purpose.id, purpose.name]))

  // The purpose breakdown (PRD section 7.7, ADR-032): Kas Utama
  // (`kind === 'main'`) is the hero above, and a closed incidental drops off
  // once it has a closed_on. Titipan has no open/closed concept, so every
  // pass_through purpose qualifies.
  const openIncidentals = new Map<number, IncidentalListItem>(
    incidentals.filter((incidental) => incidental.closed_on === null).map((incidental) => [incidental.purpose_id, incidental]),
  )
  const closedIncidentalCount = incidentals.length - openIncidentals.size
  // Kas Utama first: the hero is the fund total, envelopes and Titipan
  // included, so once money is allocated into envelopes (ADR-036) this row is
  // the only place she sees what Kas Utama itself has left - and its ADR-031
  // negative.
  const purposeRows = [
    ...balances.purposes.filter((purpose) => purpose.kind === 'main'),
    ...balances.purposes.filter(
      (purpose) => purpose.kind === 'pass_through' || (purpose.kind === 'incidental' && openIncidentals.has(purpose.id)),
    ),
  ]

  return (
    <div className="flex flex-col gap-6">
      {/* "Balance is the hero" (Design-System.md:91). The prominence comes
          from surface, space and color - a card at the system's own radius
          and card elevation, 24px of padding, the figure in Forest - not
          from a bigger number: 36px/700 tabular IS the Balance/display role,
          the top of the type scale, and inventing a larger size would put
          this screen outside the system.
          Deliberately not a filled Forest block either. Forest (#1F5D50) and
          success (#2C7756) are neighbours, so a green slab sitting directly
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
      {/* The hero and its reconciliation status are one statement - the
          balance, and whether it matches the money - so the banner tucks
          under the hero's lower edge, ahead of everything else (ADR-038's
          2026-11-02 amendment; Design-System.md's home layout). `isolate`
          keeps the hero's z-10 inside this pair: it lifts the hero over the
          banner only, never over the fixed header (also z-10, earlier in
          the page) as the page scrolls beneath it. */}
      <div className="isolate flex flex-col">
        <section
          aria-label={copy.home.balanceHeading}
          className="relative z-10 flex flex-col items-center gap-2 rounded-3xl bg-primary px-6 py-6 text-center text-primary-foreground hero-glass glass-rim"
        >
          <p className="text-base font-medium text-primary-foreground/85">{copy.home.balanceHeading}</p>
          <p className={`tabular font-bold leading-tight ${heroSize(formatIDR(balances.fund_total))}`}>{formatIDR(balances.fund_total)}</p>
        </section>
        {/* A 404 from /api/reconciliations/latest is the only signal that no
            count has ever been taken - open-lines answers [] either way, so
            the banner cannot tell "nothing open" from "never looked" on its
            own. The banner is the reconcile screen's entry point (M6.10):
            no separate button anywhere else on this screen. onReconcile is
            App.tsx's navigate('/reconcile'); Home stays router-agnostic. */}
        <ReconciliationBanner
          openLines={openLines}
          everReconciled={latest !== null}
          onClick={onReconcile}
          tucked
          footnotes={
            latest
              ? [
                  copy.home.lastChecked(formatUnixSeconds(latest.performed_at)),
                  ...(latest.has_new_entries ? [copy.home.newEntriesSince] : []),
                ]
              : []
          }
        />
      </div>

      <section className="flex flex-col gap-2">
        <SectionLabel>{copy.home.locationsHeading}</SectionLabel>
        {/* Each location opens Riwayat filtered to it (#784), as each pos
            row does - so a selisih on Tunai is one tap from what went
            through Tunai. Cards, like the pos rows, now that a row opens
            something (the 2026-10-03 plain list held only while there was
            nowhere to send it). */}
        <ul className="flex flex-col gap-2">
          {balances.accounts.map((account: AccountBalance) => (
            <li key={account.id}>
              <button type="button" onClick={() => onViewTransactionsForAccount(account.id)} className={rowClass}>
                <span className="min-w-0 truncate">{account.name}</span>
                <span className="flex shrink-0 items-center gap-2">
                  <span className="tabular font-medium">{formatIDR(account.balance)}</span>
                  <RowChevron />
                </span>
              </button>
            </li>
          ))}
        </ul>
      </section>

      {/* Incidentals are not history, they are a live balance sitting in the
          fund right now (ADR-032) - so this is entry points, not a button
          into a list. It is also where an envelope is opened and the closed
          ones are found (ADR-047): operations live where the money is. Kas
          Utama always exists, so the list is never empty once the fund is
          set up; the button renders regardless. */}
      <section className="flex flex-col gap-2">
        <SectionLabel>{copy.home.purposeBreakdownHeading}</SectionLabel>
        {purposeRows.length > 0 && (
          <ul className="flex flex-col gap-2">
            {purposeRows.map((purpose: PurposeBalance) => {
              const participation = openIncidentals.get(purpose.id)?.participation ?? null
              return (
                // Every row opens something, so every row is a button with a
                // chevron: an open envelope opens its detail view
                // (Incidentals.tsx, ?purpose=<id>); Kas Utama and Titipan open
                // their own history in Riwayat.
                <li key={purpose.id}>
                  <button
                    type="button"
                    onClick={() => (purpose.kind === 'incidental' ? onOpenIncidental(purpose.id) : onViewTransactionsFor(purpose.id))}
                    className={rowClass}
                  >
                    <span className="flex min-w-0 flex-col">
                      <span className="truncate">{purpose.name}</span>
                      {/* Who has given so far (#746), the public report's own
                          line - only for an envelope that expects anyone. */}
                      {participation && (
                        <span className="text-sm text-muted-foreground">
                          {copy.incidentals.participation.progress(participation.given, participation.expected)}
                        </span>
                      )}
                    </span>
                    {/* Same element, same sizing as ReconciliationBanner.tsx
                        directly above. */}
                    <span className="flex shrink-0 items-center gap-2">
                      <PurposeAmount balance={purpose.balance} />
                      <RowChevron />
                    </span>
                  </button>
                </li>
              )
            })}
            {/* Closed envelopes pile up over the years, so they sit one tap
                away behind a count - absent until there is one. */}
            {closedIncidentalCount > 0 && (
              <li>
                <button type="button" onClick={onViewClosedIncidentals} className={rowClass}>
                  <span className="min-w-0 truncate">{copy.incidentals.closedRow}</span>
                  <span className="flex shrink-0 items-center gap-2 text-muted-foreground">
                    <span className="tabular">{closedIncidentalCount}</span>
                    <RowChevron />
                  </span>
                </button>
              </li>
            )}
          </ul>
        )}
        {/* Outline, not filled: Beranda's one filled action is the count. */}
        <Button type="button" variant="outline" size="lg" className="self-start" onClick={() => openDialog('incidental:new')}>
          {copy.incidentals.open.heading}
        </Button>
      </section>

      <OpenIncidentalDialog
        open={dialogTarget === 'new'}
        onClose={closeDialog}
        onOpened={(opened) => onIncidentalOpened(opened.purpose_id)}
      />

      {/* What the fund owes members who fronted an expense (#406). The money
          is still inside the balance above, so this is a plain row beside
          it, never a subtraction and never an alarm - and absent at zero. */}
      {balances.owed_to_members > 0 && (
        <button type="button" onClick={onViewOwed} className={`flex items-center justify-between gap-3 ${tappableCard}`}>
          <span>{copy.home.owedToMembers}</span>
          <span className="flex items-center gap-2">
            <span className="tabular font-medium">{formatIDR(balances.owed_to_members)}</span>
            <RowChevron />
          </span>
        </button>
      )}

      {/* Everything above is the fund as it stands - balance, where it is,
          whether it matches, what is earmarked; below is what happened. One
          hairline between the two (#319). */}
      <SectionDivider />

      {/* Arus kas: the running month, two figures (ADR-038), served by the
          API from the report's own ledger function, so Beranda and the public
          report cannot disagree. Its own section, first below the divider:
          what happened this month, then the rows it happened in. Plain ink -
          a month is not a verdict. */}
      <section className="flex flex-col gap-2">
        <SectionLabel>{copy.home.cashflowHeading}</SectionLabel>
        <div className={card}>
          {/* Two columns while both figures are short; one under the other when
              either is wide enough to crowd its half (a whole figure to its
              own line, never a clipped or wrapped number). */}
          <dl
            className={`grid gap-3 ${Math.max(formatIDR(balances.month.in).length, formatIDR(balances.month.out).length) > 15 ? 'grid-cols-1' : 'grid-cols-2'}`}
          >
            <div>
              <dt className="text-sm text-muted-foreground">{copy.home.monthIn}</dt>
              <dd className="tabular font-medium">{formatIDR(balances.month.in)}</dd>
            </div>
            <div>
              <dt className="text-sm text-muted-foreground">{copy.home.monthOut}</dt>
              <dd className="tabular font-medium">{formatIDR(balances.month.out)}</dd>
            </div>
          </dl>
        </div>
      </section>

      <section className="flex flex-col gap-2">
        <SectionLabel action={{ label: copy.home.recentActivityViewAll, onClick: onViewHistory }}>
          {copy.home.recentActivityHeading}
        </SectionLabel>
        <TransactionList
          transactions={recentTransactions}
          purposeNames={purposeNames}
          emptyMessage={copy.home.recentActivityEmpty}
          onRefresh={() => run(loadHomeData, { silent: true })}
        />
      </section>
    </div>
  )
}

/**
 * The hero's type size, stepped down by how wide the figure is so it always
 * fits the card on one line: 36px for everyday figures, smaller as the
 * formatted figure grows toward Rp999.999.999.999 and its minus (19
 * characters with the word joiner). Never a wrap or an ellipsis inside the
 * number. The steps were measured (#540) when "Rp" carried a space; #724 took
 * it out, so each bound sits one lower and every figure keeps its step.
 */
function heroSize(figure: string): string {
  if (figure.length <= 14) return 'text-[36px]'
  if (figure.length <= 16) return 'text-[32px]'
  if (figure.length <= 19) return 'text-[28px]'
  return 'text-2xl'
}

/**
 * A purpose-breakdown row's amount: a negative balance - a shortfall an
 * incidental's disbursements ran ahead of its collections, covered from Kas
 * Utama per ADR-031 - is legible without reading as an alarm. `--attention`
 * (terracotta) on the figure is the whole treatment: no icon, no extra word,
 * the restraint of the reconciliation banner's "selisih" state.
 */
function PurposeAmount({ balance }: { balance: number }) {
  return <span className={`tabular font-medium ${balance < 0 ? 'text-attention-text' : ''}`}>{formatIDR(balance)}</span>
}
