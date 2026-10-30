import { useCallback, useEffect, useRef } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate, useSearchParams } from 'react-router-dom'

import OfflineBanner from '@/components/states/OfflineBanner'
import UpdateBanner from '@/components/states/UpdateBanner'
import Shell from '@/components/Shell'
import { ToastProvider } from '@/components/toast/ToastProvider'
import { useArrivalToast } from '@/components/toast/useArrivalToast'
import type { ArrivalToast } from '@/components/toast/useArrivalToast'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import Register from '@/screens/Register'
import Login from '@/screens/Login'
import Setup from '@/screens/Setup/Setup'
import RecordTransaction from '@/screens/RecordTransaction'
import type { Recorded } from '@/screens/RecordTransaction'
import Reconcile from '@/screens/Reconcile'
import DuesTierScreen from '@/screens/DuesTier'
import Incidentals from '@/screens/Incidentals'
import History from '@/screens/History/History'
import Transactions from '@/screens/History/Transactions'
import Reimbursements from '@/screens/History/Reimbursements'
import CorrectClaim from '@/screens/Reimbursements/Correct'
import type { TalanganState } from '@/screens/Reimbursements/helpers'
import Reconciliations from '@/screens/History/Reconciliations'
import DuesStatus from '@/screens/Dues/Status'
import PaymentHistory from '@/screens/Dues/PaymentHistory'
import Home from '@/screens/Home'
import Members from '@/screens/Members'
import Settings from '@/screens/Settings'
import { ClosedIncidentals } from '@/screens/Settings/Incidentals'
import { demoLoginOf, getSession } from '@/lib/auth'
import { getFund } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import { DemoMode } from '@/lib/demoMode'
import { useRefetchKey } from '@/lib/useDialogParam'
import type { SessionStatus, AuthUser } from '@/lib/auth'
import type { Fund } from '@/lib/setup'
import { copy } from '@/copy/id'
import { errorCode } from '@/lib/api'

/**
 * Router root for the whole SPA.
 *
 * On mount it probes GET /api/session once and routes off the result:
 * `has_account: false` renders Register, `has_account: true, authenticated:
 * false` renders Login, and `authenticated: true` hands off to AuthedGate,
 * which probes GET /api/fund the same way: 404 renders Setup, 200 renders the
 * everyday-loop routes inside Shell. A successful register or login calls back
 * here and optimistically marks the session probe authenticated rather than
 * re-fetching /api/session, so the handoff needs no reload; Setup's onDone
 * re-probes GET /api/fund the same way.
 */
export default function App() {
  const [state, run] = useApi<SessionStatus>()

  // Probed once, on boot: `run` is a stable useCallback (useApi.ts), so this
  // effect never re-fires on its own. A session established afterward
  // (register/login) updates local state directly instead.
  useEffect(() => {
    void run(getSession)
  }, [run])

  // The last probe, for the handoffs below: a demo's login rides on the probe
  // (ADR-045), and a logout must land on a Login that still knows it.
  const probe = useRef<SessionStatus | undefined>(undefined)
  useEffect(() => {
    probe.current = state.data
  }, [state.data])

  function handleAuthenticated(_user: AuthUser) {
    // The server session is already established (register.go and login.go both
    // RenewToken + Put before answering 2xx) - this syncs the client's view of
    // /api/session without a second round trip.
    void run(async () => ({ ...demoFields(probe.current), authenticated: true, has_account: true }))
  }

  // Mirror image of handleAuthenticated, for the shell's logout button:
  // POST /api/logout has already destroyed the server session by the time
  // Shell calls this. has_account stays true - the account exists, only the
  // session is gone - so this lands on Login, never Register. useCallback
  // because Shell's success effect depends on this identity.
  const handleLoggedOut = useCallback(() => {
    void run(async () => ({ ...demoFields(probe.current), authenticated: false, has_account: true }))
  }, [run])

  return (
    <BrowserRouter>
      <OfflineBanner />
      <UpdateBanner />
      <Routes>
        <Route
          path="*"
          element={
            <AuthGate
              state={state}
              onRetry={() => void run(getSession)}
              onAuthenticated={handleAuthenticated}
              onLoggedOut={handleLoggedOut}
            />
          }
        />
      </Routes>
    </BrowserRouter>
  )
}

function AuthGate({
  state,
  onRetry,
  onAuthenticated,
  onLoggedOut,
}: {
  state: ReturnType<typeof useApi<SessionStatus>>[0]
  onRetry: () => void
  onAuthenticated: (user: AuthUser) => void
  onLoggedOut: () => void
}) {
  if (state.status === 'idle' || state.status === 'loading') {
    return (
      <main className="flex min-h-dvh items-center justify-center p-6">
        <Loading />
      </main>
    )
  }

  if (state.status === 'error' || !state.data) {
    return (
      <main className="flex min-h-dvh items-center justify-center p-6">
        {state.error && <ErrorState error={state.error} onRetry={onRetry} />}
      </main>
    )
  }

  if (!state.data.has_account) {
    return <Register onRegistered={onAuthenticated} />
  }

  const demo = demoLoginOf(state.data)

  if (!state.data.authenticated) {
    return <Login onLoggedIn={onAuthenticated} demo={demo} />
  }

  return (
    <DemoMode.Provider value={demo !== null}>
      <ToastProvider>
        <AuthedGate onLoggedOut={onLoggedOut} />
      </ToastProvider>
    </DemoMode.Provider>
  )
}

/** The part of a probe that outlives a login or a logout: the demo's shared
 * login, if the server sent one. */
function demoFields(session: SessionStatus | undefined): Pick<SessionStatus, 'demo_email' | 'demo_password'> {
  return session ? { demo_email: session.demo_email, demo_password: session.demo_password } : {}
}

/** The envelope detail's origin, carried in the navigation's state. */
interface EnvelopeOriginState {
  from: 'home'
}

/** What a successful record hands to home through the history entry it
 * creates. photoFailed is true only when a photo was picked and the
 * transaction posted but the receipt upload failed - never set for a
 * transfer, which has no photo field. */
interface HomeState {
  // 'dues': a dues payment recorded through Catat's Iuran.
  // 'reimbursement': a Keluar ticked "ditalangi" - a Talangan claim.
  recorded: Recorded | 'dues'
  photoFailed?: boolean
}

/** The same idiom for a dues payment: the confirmation belongs to the one
 * history entry that navigation creates, not to this component. */
interface DuesState {
  duesRecorded: true
}

/** A dues payment opened from the status matrix rather than from Catat's
 * Iuran: saving and Kembali both go back to the matrix, the door she came in
 * by. */
interface RecordFromDuesState {
  /** The matrix's own query string ("?view=grid&period=2026-08", or ""), so
   * the way back lands on the same view and month. */
  fromDues: { search: string }
}

/** The confirmation a navigation hands to the screen it lands on, if any: the
 * record form's (to Beranda) or a dues payment's (back to the status matrix). A
 * failed photo upload replaces the ordinary success line rather than joining it
 * - the sentence already says the transaction is saved, so repeating
 * successIn/successOut would say the same thing twice in two tones - and is the
 * attention tone: the transaction is fine, but something did not go through. */
function pickArrivalToast(state: unknown): ArrivalToast | null {
  const s = state as Partial<HomeState & DuesState> | null
  if (s?.duesRecorded === true) return { tone: 'success', text: copy.dues.payment.success }
  const recorded = s?.recorded
  if (recorded === undefined) return null
  if (s?.photoFailed === true) {
    return {
      tone: 'attention',
      text: recorded === 'reimbursement' ? copy.receipts.reimbursementPhotoFailed : copy.receipts.transactionPhotoFailed,
    }
  }
  const text =
    recorded === 'reimbursement'
      ? copy.record.successFronted
      : recorded === 'in'
        ? copy.record.successIn
        : recorded === 'out'
          ? copy.record.successOut
          : recorded === 'transfer'
            ? copy.record.successTransfer
            : recorded === 'purpose'
              ? copy.record.successPurposeMove
              : recorded === 'dues'
                ? copy.dues.payment.success
                : null
  return text === null ? null : { tone: 'success', text }
}

/** "YYYY-MM" from a URL value, else null. */
function periodParam(value: string | null): string | null {
  return value !== null && /^\d{4}-(0[1-9]|1[0-2])$/.test(value) ? value : null
}

/**
 * Once GET /api/session says authenticated: true, this decides setup-vs-home
 * from GET /api/fund - the same probe-once shape as the session probe above.
 * A 404 (no fund yet) renders Setup; any other success renders the
 * everyday-loop routes inside Shell, titled with the fund's name. Register,
 * Login and the setup wizard stay outside it deliberately: there is no fund
 * to name in the header yet, and no session worth a logout button.
 */
function AuthedGate({ onLoggedOut }: { onLoggedOut: () => void }) {
  const [state, run] = useApi<Fund>()
  const navigate = useNavigate()
  const location = useLocation()
  // location.key, minus the entries a dialog pushes or clears - so opening a
  // dialog never reloads the list under it (useRefetchKey).
  const refetchKey = useRefetchKey()
  const [searchParams, setSearchParams] = useSearchParams()

  // ?purpose=<id> - shared by three routes, read here for two of them
  // (History/Transactions.tsx reads its own). /record?purpose=<id> is
  // Incidentals.tsx's contribute/disburse entry point, reusing this form; a
  // missing or non-numeric value falls back to RecordTransaction's default
  // (the `kind: "main"` row) as if absent. /incidentals?purpose=<id> is
  // Home's purpose-breakdown row and Pengaturan's incidentals cards: the only
  // way that route renders a detail view now that its list is retired.
  // /history/transactions?purpose=<id> is the filter ADR-032 makes a closed
  // envelope's only route to its own record.
  const rawPurpose = searchParams.get('purpose')
  const parsedPurpose = rawPurpose === null || rawPurpose.trim() === '' ? NaN : Number(rawPurpose)
  const initialPurposeId = Number.isInteger(parsedPurpose) && parsedPurpose > 0 ? parsedPurpose : null

  // /record?member=<id> (ADR-034): Incidentals.tsx's participation row action
  // pre-chooses the contributor as ?purpose= pre-chooses the envelope - a
  // missing or unparseable value falls back to nobody named.
  const rawMember = searchParams.get('member')
  const parsedMember = rawMember === null || rawMember.trim() === '' ? NaN : Number(rawMember)
  const initialMemberId = Number.isInteger(parsedMember) && parsedMember > 0 ? parsedMember : null

  // /dues-tiers?tier=<id> is a tier's screen, reached from the Kategori
  // card in Pengaturan only - parsed like /incidentals?purpose=<id> above.
  const rawTier = searchParams.get('tier')
  const parsedTier = rawTier === null || rawTier.trim() === '' ? NaN : Number(rawTier)
  const tierId = Number.isInteger(parsedTier) && parsedTier > 0 ? parsedTier : null

  // /reimbursements/correct?id=<id> is one claim's screen, reached from
  // Talangan's row only - parsed like ?tier= above. Without a usable id there
  // is nothing to show, so it redirects to the tab. /reimbursements/settle?id=
  // redirects to the tab's settle dialog (ADR-046).
  const rawClaim = searchParams.get('id')
  const parsedClaim = rawClaim === null || rawClaim.trim() === '' ? NaN : Number(rawClaim)
  const claimId = Number.isInteger(parsedClaim) && parsedClaim > 0 ? parsedClaim : null
  const backToTalangan = () => navigate('/history/reimbursements')
  const talanganDone = (reimbursementDone: TalanganState['reimbursementDone']) =>
    navigate('/history/reimbursements', { state: { reimbursementDone } satisfies TalanganState })

  useEffect(() => {
    void run(getFund)
  }, [run])

  useArrivalToast(pickArrivalToast)

  // The record form's onRecorded contract (RecordTransaction.tsx): fired once
  // after a successful POST /api/transactions. Home reads refetchKey to
  // refetch on this navigation, so the new entry shows in recent activity; the
  // success message stays here, not duplicated inside Home.
  //
  // Carried as router location state, not component state: state on this
  // component outlives the navigation, so recording once and later opening
  // the form again would show the old confirmation on the way back. A history
  // entry's state belongs to that entry alone, the lifetime this message
  // wants.
  function handleRecorded(recorded: Recorded, photoFailed?: boolean) {
    navigate('/', { state: { recorded, photoFailed } satisfies HomeState })
  }

  if (state.status === 'idle' || state.status === 'loading') {
    return (
      <main className="flex min-h-dvh items-center justify-center p-6">
        <Loading />
      </main>
    )
  }

  if (state.status === 'error') {
    if (errorCode(state.error) === 'not_found') {
      return <Setup onDone={() => void run(getFund)} />
    }
    return (
      <main className="flex min-h-dvh items-center justify-center p-6">
        {state.error && <ErrorState error={state.error} onRetry={() => void run(getFund)} />}
      </main>
    )
  }

  const title = state.data?.name ?? copy.app.name
  // Where the envelope detail was opened from: Beranda says so in the history
  // entry's state, Pengaturan says nothing, and a deep link or reload has no
  // state at all - both of those lead back to Pengaturan.
  const envelopeFromHome = (location.state as EnvelopeOriginState | null)?.from === 'home'
  const recordFromDuesState = (location.state as RecordFromDuesState | null)?.fromDues
  const recordFromDues = recordFromDuesState !== undefined && recordFromDuesState !== null && typeof recordFromDuesState === 'object'
  const duesReturn = '/dues' + (recordFromDues ? recordFromDuesState.search : '')
  const recordDuesPeriod = periodParam(searchParams.get('period'))
  const recordDues = searchParams.get('type') === 'dues'
  // "Pengeluaran ini ditalangi": form mode is a route (ADR-032), like
  // ?type=dues. Written with replace so ticking it adds no history entry.
  const recordFronted = searchParams.get('fronted') === '1'
  const setRecordFronted = (next: boolean) =>
    setSearchParams(
      (prev) => {
        const params = new URLSearchParams(prev)
        if (next) params.set('fronted', '1')
        else params.delete('fronted')
        return params
      },
      { replace: true },
    )
  return (
    <Routes>
      <Route
        path="/record"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <RecordTransaction
              onRecorded={handleRecorded}
              onCancel={() => navigate('/')}
              initialPurposeId={initialPurposeId}
              initialMemberId={initialMemberId}
              initialDues={recordDues}
              initialDuesPeriod={recordDuesPeriod}
              fronted={recordFronted}
              onFrontedChange={setRecordFronted}
              onDuesRecorded={() =>
                recordFromDues
                  ? navigate(duesReturn, { state: { duesRecorded: true } satisfies DuesState })
                  : navigate('/', { state: { recorded: 'dues' } satisfies HomeState })
              }
              onDuesCancel={recordFromDues ? () => navigate(duesReturn) : undefined}
            />
          </Shell>
        }
      />
      <Route
        path="/reconcile"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <Reconcile onDone={() => navigate('/')} onCancel={() => navigate('/')} />
          </Shell>
        }
      />
      {/* Riwayat (M6.23, ADR-032): a tab strip over an Outlet, one Shell for
          every tab. Iuran's tab is the payment history alone (#228); the
          status matrix is its own screen at /dues, below. */}
      <Route
        path="/history"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <History />
          </Shell>
        }
      >
        <Route index element={<Navigate to="transactions" replace />} />
        <Route path="transactions" element={<Transactions refetchKey={refetchKey} />} />
        <Route path="dues" element={<PaymentHistory onOpenStatus={() => navigate('/dues')} refetchKey={refetchKey} />} />
        <Route path="reimbursements" element={<Reimbursements refetchKey={refetchKey} />} />
        <Route path="reconciliations" element={<Reconciliations refetchKey={refetchKey} />} />
      </Route>
      {/* The dues status roster, its own screen again (#228): one month's
          reading does not share a page with an unbounded list, so it sits
          one row away from Riwayat's Iuran tab rather than on top of it
          (ADR-032). Back returns to that tab, not home. */}
      <Route
        path="/dues"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <DuesStatus
              onBack={() => navigate('/history/dues')}
              onRecordPayment={(target) =>
                navigate(target ? `/record?type=dues&member=${target.memberId}&period=${target.period}` : '/record?type=dues', {
                  state: { fromDues: { search: location.search } } satisfies RecordFromDuesState,
                })
              }
              refetchKey={refetchKey}
            />
          </Shell>
        }
      />
      {/* The reimbursements screen's own former address (through M6.24) -
          Talangan now lives at /history/reimbursements (#226, ADR-032),
          same redirect precedent as /dues above. */}
      <Route path="/reimbursements" element={<Navigate to="/history/reimbursements" replace />} />
      {/* #368, ADR-032: recording a claim is Catat's Keluar with "Pengeluaran
          ini ditalangi" ticked, so its own former address redirects there
          (same precedent as /dues/payment below). Correct is a
          screen: cancel and success both return to the tab, and success
          hands its confirmation back through router state. */}
      <Route path="/reimbursements/new" element={<Navigate to="/record?fronted=1" replace />} />
      {/* ADR-046: settling is a dialog on the tab, so the screen's old address
          is a redirect to it, the dialog open - a bookmark or an old link
          still lands on the claim. Without a usable id, the tab alone. */}
      <Route
        path="/reimbursements/settle"
        element={<Navigate to={claimId === null ? '/history/reimbursements' : `/history/reimbursements?edit=settle:${claimId}`} replace />}
      />
      <Route
        path="/reimbursements/correct"
        element={
          claimId === null ? (
            <Navigate to="/history/reimbursements" replace />
          ) : (
            <Shell title={title} onLoggedOut={onLoggedOut}>
              <CorrectClaim claimId={claimId} onDone={() => talanganDone('corrected')} onCancel={backToTalangan} />
            </Shell>
          )
        }
      />
      {/* The dues payment form's own former address (M6.13) - it is Catat's
          fourth Jenis now (#315), opened with Iuran chosen and the matrix as
          the door to return through. */}
      <Route
        path="/dues/payment"
        element={<Navigate to="/record?type=dues" state={{ fromDues: { search: '' } } satisfies RecordFromDuesState} replace />}
      />
      <Route
        path="/members"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <Members />
          </Shell>
        }
      />
      <Route
        path="/settings"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            {/* Pengaturan reads the fund this gate already holds rather than
                fetching it again, and a change lands here silently: a visible
                run would drop to the loading screen and remount every section. */}
            {state.data && (
              <Settings
                fund={state.data}
                onFundChanged={(fund) => void run(async () => fund, { silent: true })}
                refreshFund={() => run(getFund, { silent: true })}
              />
            )}
          </Shell>
        }
      />
      {/* #263/ADR-032: the list view is retired, so /incidentals is only
          ever a detail view reached with ?purpose=<id> (a Beranda row, or a
          card in Pengaturan's own section). Without one - a bare visit, or
          an unparseable value - there is nothing here to show, so this
          redirects to Pengaturan, the screen that now owns that list. */}
      {/* Closed envelopes, gathered off Pengaturan (#319). */}
      <Route
        path="/incidentals/closed"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <ClosedIncidentals onBack={() => navigate('/settings')} onOpen={(purposeId) => navigate(`/incidentals?purpose=${purposeId}`)} />
          </Shell>
        }
      />
      <Route
        path="/incidentals"
        element={
          initialPurposeId === null ? (
            <Navigate to="/settings" replace />
          ) : (
            <Shell title={title} onLoggedOut={onLoggedOut}>
              <Incidentals
                onBack={() => navigate(envelopeFromHome ? '/' : '/settings')}
                backLabel={envelopeFromHome ? copy.incidentals.detail.backToHome : copy.incidentals.detail.backToSettings}
                onRecordFor={(purposeId, memberId) =>
                  navigate(`/record?purpose=${purposeId}${memberId !== undefined ? `&member=${memberId}` : ''}`)
                }
                onViewTransactionsFor={(purposeId) => navigate(`/history/transactions?purpose=${purposeId}`)}
                purposeId={initialPurposeId}
              />
            </Shell>
          )
        }
      />
      {/* #285: a tier holds a name and a price history, which a dialog
          has neither the height for nor one unambiguous way out of. Like
          /incidentals, this route is only ever a detail view: without a
          usable ?tier=<id> there is nothing to show, so it redirects to
          Pengaturan, the screen that owns the list. */}
      <Route
        path="/dues-tiers"
        element={
          tierId === null ? (
            <Navigate to="/settings" replace />
          ) : (
            <Shell title={title} onLoggedOut={onLoggedOut}>
              <DuesTierScreen tierId={tierId} onBack={() => navigate('/settings')} />
            </Shell>
          )
        }
      />
      <Route
        path="*"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <Home
              refetchKey={refetchKey}
              onReconcile={() => navigate('/reconcile')}
              onOpenIncidental={(purposeId) =>
                navigate(`/incidentals?purpose=${purposeId}`, { state: { from: 'home' } satisfies EnvelopeOriginState })
              }
              onViewTransactionsFor={(purposeId) => navigate(`/history/transactions?purpose=${purposeId}`)}
              onViewHistory={() => navigate('/history/transactions')}
              onViewOwed={() => navigate('/history/reimbursements')}
            />
          </Shell>
        }
      />
    </Routes>
  )
}
