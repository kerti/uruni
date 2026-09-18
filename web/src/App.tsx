import { useCallback, useEffect } from 'react'
import { CircleCheck } from 'lucide-react'
import { BrowserRouter, Navigate, Route, Routes, useLocation, useNavigate, useSearchParams } from 'react-router-dom'

import OfflineBanner from '@/components/states/OfflineBanner'
import UpdateBanner from '@/components/states/UpdateBanner'
import Shell from '@/components/Shell'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import Register from '@/screens/Register'
import Login from '@/screens/Login'
import Setup from '@/screens/Setup/Setup'
import RecordTransaction from '@/screens/RecordTransaction'
import type { Direction } from '@/screens/RecordTransaction'
import Reconcile from '@/screens/Reconcile'
import DuesTierScreen from '@/screens/DuesTier'
import Incidentals from '@/screens/Incidentals'
import History from '@/screens/History/History'
import Transactions from '@/screens/History/Transactions'
import Reimbursements from '@/screens/History/Reimbursements'
import Reconciliations from '@/screens/History/Reconciliations'
import DuesStatus from '@/screens/Dues/Status'
import RecordDuesPayment from '@/screens/Dues/RecordPayment'
import PaymentHistory from '@/screens/Dues/PaymentHistory'
import Home from '@/screens/Home'
import Members from '@/screens/Members'
import Settings from '@/screens/Settings'
import { getSession } from '@/lib/auth'
import { getFund } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { SessionStatus, AuthUser } from '@/lib/auth'
import type { Fund } from '@/lib/setup'
import { copy } from '@/copy/id'

/**
 * Router root for the whole SPA (M6.2, extended by M6.4's session probe,
 * M6.5's fund probe and M6.8/M6.9's everyday-loop routes).
 *
 * On mount it probes GET /api/session once and routes off the result:
 * `has_account: false` renders Register, `has_account: true,
 * authenticated: false` renders Login, and `authenticated: true` hands off
 * to AuthedGate, which probes GET /api/fund the same way: 404 renders Setup
 * (M6.5), 200 renders the everyday-loop routes inside M6.6's Shell - home
 * (M6.9) and "/record". A successful register or login calls back into this
 * component and optimistically marks the session probe authenticated rather
 * than re-fetching /api/session, so the handoff needs no reload; Setup's
 * onDone re-probes GET /api/fund the same way once setup finishes.
 */
export default function App() {
  const [state, run] = useApi<SessionStatus>()

  // Probed once, on boot: `run` is a stable useCallback (useApi.ts), so this
  // effect never re-fires on its own. A session established afterward
  // (register/login) updates local state directly instead, see below.
  useEffect(() => {
    void run(getSession)
  }, [run])

  function handleAuthenticated(_user: AuthUser) {
    // The server session is already established by this point (register.go
    // and login.go both RenewToken + Put before answering 2xx) - this just
    // brings the client's view of /api/session's shape in sync with that,
    // without paying for a second round trip to learn what the request
    // that just succeeded already told us.
    void run(async () => ({ authenticated: true, has_account: true }))
  }

  // Mirror image of handleAuthenticated, for the shell's logout button
  // (M6.6): POST /api/logout has already destroyed the server session by the
  // time Shell calls this, so the client's view is brought back in line
  // without a second round trip. has_account stays true - the account still
  // exists, it is only the session that is gone - so this lands on Login,
  // never back on Register. useCallback because Shell's success effect
  // depends on this identity.
  const handleLoggedOut = useCallback(() => {
    void run(async () => ({ authenticated: false, has_account: true }))
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

  if (!state.data.authenticated) {
    return <Login onLoggedIn={onAuthenticated} />
  }

  return <AuthedGate onLoggedOut={onLoggedOut} />
}

/** What a successful record hands to home through the history entry it creates. */
interface HomeState {
  recorded: Direction
}

/** The same idiom for a dues payment (M6.13): the confirmation belongs to
 * the one history entry that navigation creates, not to this component. */
interface DuesState {
  duesRecorded: true
}

/**
 * Once GET /api/session says authenticated: true, this decides setup-vs-home
 * from GET /api/fund - the same probe-once shape as AuthGate's own session
 * probe above, just one layer in. A 404 (no fund yet) renders Setup; any
 * other success renders the everyday-loop routes: "/" is Home (M6.9),
 * "/record" is RecordTransaction (M6.8), both inside Shell.
 *
 * The 200 branch is also where M6.6's Shell starts: everything past setup
 * renders inside it, titled with the fund's own name. Register, Login and
 * the setup wizard stay outside it deliberately - there is no fund to name
 * in the header yet, and no session worth offering a logout button for.
 */
function AuthedGate({ onLoggedOut }: { onLoggedOut: () => void }) {
  const [state, run] = useApi<Fund>()
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams] = useSearchParams()

  // ?purpose=<id> - shared by three routes, and read here for two of them
  // (History/Transactions.tsx reads its own). /record?purpose=<id> is
  // Incidentals.tsx's own contribute/disburse entry point, reusing this
  // form entirely rather than a second copy of it (M6.19); a missing or
  // non-numeric value falls back to RecordTransaction's own default (the
  // `kind: "main"` row) exactly as if the param were absent.
  // /incidentals?purpose=<id> is Home's purpose-breakdown row (M6.33) and
  // Pengaturan's incidentals cards (#263): the only way that route ever
  // renders a detail view now that its list is retired - see the route
  // below. /history/transactions?purpose=<id> is the third (#262) - the
  // filter ADR-032 makes a closed envelope's only route to its own record,
  // linked from the detail screen this one renders.
  const rawPurpose = searchParams.get('purpose')
  const parsedPurpose = rawPurpose === null || rawPurpose.trim() === '' ? NaN : Number(rawPurpose)
  const initialPurposeId = Number.isInteger(parsedPurpose) && parsedPurpose > 0 ? parsedPurpose : null

  // #285: /dues-tiers?tier=<id> is a golongan's own screen, reached from the
  // Golongan card in Pengaturan and from nowhere else - the same shape as
  // /incidentals?purpose=<id> above, and parsed the same way.
  const rawTier = searchParams.get('tier')
  const parsedTier = rawTier === null || rawTier.trim() === '' ? NaN : Number(rawTier)
  const tierId = Number.isInteger(parsedTier) && parsedTier > 0 ? parsedTier : null

  useEffect(() => {
    void run(getFund)
  }, [run])

  // The record form's own onRecorded contract (RecordTransaction.tsx): fired
  // once after a successful POST /api/transactions. Home reads
  // location.key (below) to refetch its data on this navigation, so the new
  // entry is visible in recent activity without a manual refresh; the
  // success message itself stays here, not duplicated inside Home.
  //
  // Carried as router location state, not component state: state on this
  // component outlives the navigation, so recording once and later opening
  // the form again would show the old confirmation on the way back. A
  // history entry's state belongs to that entry alone, which is exactly the
  // lifetime this message wants.
  function handleRecorded(direction: Direction) {
    navigate('/', { state: { recorded: direction } satisfies HomeState })
  }

  if (state.status === 'idle' || state.status === 'loading') {
    return (
      <main className="flex min-h-dvh items-center justify-center p-6">
        <Loading />
      </main>
    )
  }

  if (state.status === 'error') {
    if (state.error?.code === 'not_found') {
      return <Setup onDone={() => void run(getFund)} />
    }
    return (
      <main className="flex min-h-dvh items-center justify-center p-6">
        {state.error && <ErrorState error={state.error} onRetry={() => void run(getFund)} />}
      </main>
    )
  }

  const title = state.data?.name ?? copy.app.name
  const recorded = (location.state as HomeState | null)?.recorded
  const duesRecorded = (location.state as DuesState | null)?.duesRecorded === true
  const successMessage =
    recorded === 'in'
      ? copy.record.successIn
      : recorded === 'out'
        ? copy.record.successOut
        : recorded === 'transfer'
          ? copy.record.successTransfer
          : null

  return (
    <Routes>
      <Route
        path="/record"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <RecordTransaction onRecorded={handleRecorded} onCancel={() => navigate('/')} initialPurposeId={initialPurposeId} />
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
        <Route path="transactions" element={<Transactions refetchKey={location.key} />} />
        <Route path="dues" element={<PaymentHistory onOpenStatus={() => navigate('/dues')} refetchKey={location.key} />} />
        <Route path="reimbursements" element={<Reimbursements refetchKey={location.key} />} />
        <Route path="reconciliations" element={<Reconciliations refetchKey={location.key} />} />
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
              onRecordPayment={() => navigate('/dues/payment')}
              refetchKey={location.key}
              notice={duesRecorded ? copy.dues.payment.success : null}
            />
          </Shell>
        }
      />
      {/* The reimbursements screen's own former address (through M6.24) -
          Talangan now lives at /history/reimbursements (#226, ADR-032),
          same redirect precedent as /dues above. */}
      <Route path="/reimbursements" element={<Navigate to="/history/reimbursements" replace />} />
      <Route
        path="/dues/payment"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <RecordDuesPayment
              onRecorded={() => navigate('/dues', { state: { duesRecorded: true } satisfies DuesState })}
              onCancel={() => navigate('/dues')}
            />
          </Shell>
        }
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
            <Settings onFundRenamed={(fund) => void run(async () => fund)} />
          </Shell>
        }
      />
      {/* #263/ADR-032: the list view is retired, so /incidentals is only
          ever a detail view reached with ?purpose=<id> (a Beranda row, or a
          card in Pengaturan's own section). Without one - a bare visit, or
          an unparseable value - there is nothing here to show, so this
          redirects to Pengaturan, the screen that now owns that list. */}
      <Route
        path="/incidentals"
        element={
          initialPurposeId === null ? (
            <Navigate to="/settings" replace />
          ) : (
            <Shell title={title} onLoggedOut={onLoggedOut}>
              <Incidentals
                onBack={() => navigate('/settings')}
                onRecordFor={(purposeId) => navigate(`/record?purpose=${purposeId}`)}
                onViewTransactionsFor={(purposeId) => navigate(`/history/transactions?purpose=${purposeId}`)}
                purposeId={initialPurposeId}
              />
            </Shell>
          )
        }
      />
      {/* #285: a golongan holds a name and a price history, which a dialog
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
            {successMessage && (
              <p role="status" className="mb-4 flex items-center gap-2 text-success">
                <CircleCheck aria-hidden="true" />
                {successMessage}
              </p>
            )}
            <Home
              refetchKey={location.key}
              onReconcile={() => navigate('/reconcile')}
              onOpenIncidental={(purposeId) => navigate(`/incidentals?purpose=${purposeId}`)}
              onViewHistory={() => navigate('/history/transactions')}
            />
          </Shell>
        }
      />
    </Routes>
  )
}
