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
import Reconcile from '@/screens/Reconcile'
import Incidentals from '@/screens/Incidentals'
import History from '@/screens/History/History'
import Transactions from '@/screens/History/Transactions'
import Reimbursements from '@/screens/History/Reimbursements'
import Reconciliations from '@/screens/History/Reconciliations'
import DuesStatus from '@/screens/Dues/Status'
import RecordDuesPayment from '@/screens/Dues/RecordPayment'
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
  recorded: 'in' | 'out'
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

  // /record?purpose=<id> - Incidentals.tsx's own contribute/disburse entry
  // point, reusing this form entirely rather than a second copy of it
  // (M6.19). A missing or non-numeric value falls back to
  // RecordTransaction's own default (the `kind: "main"` row) exactly as if
  // the param were absent.
  const rawPurpose = searchParams.get('purpose')
  const parsedPurpose = rawPurpose === null || rawPurpose.trim() === '' ? NaN : Number(rawPurpose)
  const initialPurposeId = Number.isInteger(parsedPurpose) && parsedPurpose > 0 ? parsedPurpose : null

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
  function handleRecorded(direction: 'in' | 'out') {
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
  const successMessage = recorded === 'in' ? copy.record.successIn : recorded === 'out' ? copy.record.successOut : null

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
          every tab. /dues kept its own route below, redirecting here rather
          than vanishing - nothing that was reachable before this slice may
          stop being reachable mid-milestone. */}
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
        <Route
          path="dues"
          element={
            <DuesStatus
              onBack={() => navigate('/')}
              onRecordPayment={() => navigate('/dues/payment')}
              refetchKey={location.key}
              notice={duesRecorded ? copy.dues.payment.success : null}
            />
          }
        />
        <Route path="reimbursements" element={<Reimbursements refetchKey={location.key} />} />
        <Route path="reconciliations" element={<Reconciliations refetchKey={location.key} />} />
      </Route>
      {/* The dues status roster's own former address (through M6.22) -
          Iuran now lives at /history/dues (ADR-032), and this redirect is
          what keeps a bookmark or an old link working. */}
      <Route path="/dues" element={<Navigate to="/history/dues" replace />} />
      {/* The reimbursements screen's own former address (through M6.24) -
          Talangan now lives at /history/reimbursements (#226, ADR-032),
          same redirect precedent as /dues above. */}
      <Route path="/reimbursements" element={<Navigate to="/history/reimbursements" replace />} />
      <Route
        path="/dues/payment"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <RecordDuesPayment
              onRecorded={() => navigate('/history/dues', { state: { duesRecorded: true } satisfies DuesState })}
              onCancel={() => navigate('/history/dues')}
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
      <Route
        path="/incidentals"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <Incidentals onBack={() => navigate('/')} onRecordFor={(purposeId) => navigate(`/record?purpose=${purposeId}`)} />
          </Shell>
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
              onViewIncidentals={() => navigate('/incidentals')}
              onViewHistory={() => navigate('/history/transactions')}
            />
          </Shell>
        }
      />
    </Routes>
  )
}
