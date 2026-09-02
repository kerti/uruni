import { useCallback, useEffect, useState } from 'react'
import { CircleCheck, Plus, TriangleAlert } from 'lucide-react'
import { BrowserRouter, Link, Route, Routes, useLocation, useNavigate } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import OfflineBanner from '@/components/states/OfflineBanner'
import UpdateBanner from '@/components/states/UpdateBanner'
import Shell from '@/components/Shell'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import Register from '@/screens/Register'
import Login from '@/screens/Login'
import Setup from '@/screens/Setup/Setup'
import RecordTransaction from '@/screens/RecordTransaction'
import { getSession } from '@/lib/auth'
import { getFund } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { SessionStatus, AuthUser } from '@/lib/auth'
import type { Fund } from '@/lib/setup'
import { copy } from '@/copy/id'

/**
 * Router root for the whole SPA (M6.2, extended by M6.4's session probe,
 * M6.5's fund probe and M6.8's everyday-loop routes). SmokePage is still a
 * placeholder M6.9 replaces once home lands.
 *
 * On mount it probes GET /api/session once and routes off the result:
 * `has_account: false` renders Register, `has_account: true,
 * authenticated: false` renders Login, and `authenticated: true` hands off
 * to AuthedGate, which probes GET /api/fund the same way: 404 renders Setup
 * (M6.5), 200 renders the everyday-loop routes inside M6.6's Shell - home
 * (still SmokePage until M6.9) and "/catat". A successful register or login calls back into this
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

/**
 * Once GET /api/session says authenticated: true, this decides setup-vs-home
 * from GET /api/fund - the same probe-once shape as AuthGate's own session
 * probe above, just one layer in. A 404 (no fund yet) renders Setup; any
 * other success renders the everyday-loop routes (M6.8): "/" is home
 * (still SmokePage until M6.9), "/catat" is RecordTransaction, both inside
 * Shell.
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

  useEffect(() => {
    void run(getFund)
  }, [run])

  // The record form's own onRecorded contract (RecordTransaction.tsx): fired
  // once after a successful POST /api/transactions. Home has no
  // recent-activity list yet (M6.9's job), so a success banner is the whole
  // of this slice's "the write landed".
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
  const successMessage = recorded === 'in' ? copy.record.successIn : recorded === 'out' ? copy.record.successOut : null

  return (
    <Routes>
      <Route
        path="/catat"
        element={
          <Shell title={title} onLoggedOut={onLoggedOut}>
            <RecordTransaction onRecorded={handleRecorded} onCancel={() => navigate('/')} />
          </Shell>
        }
      />
      <Route
        path="*"
        element={
          <Shell
            title={title}
            onLoggedOut={onLoggedOut}
            action={
              <Button asChild size="icon" className="size-14 rounded-full shadow-floating" aria-label={copy.record.addAction}>
                <Link to="/catat">
                  <Plus aria-hidden="true" className="size-6" />
                </Link>
              </Button>
            }
          >
            {successMessage && (
              <p role="status" className="mb-4 flex items-center gap-2 text-success">
                <CircleCheck aria-hidden="true" />
                {successMessage}
              </p>
            )}
            <SmokePage />
          </Shell>
        }
      />
    </Routes>
  )
}

type Health = 'idle' | 'checking' | 'online' | 'offline'

/**
 * The M1 smoke page, carried over unpolished as a connectivity check a human
 * can still run by hand. Nothing here is meant to survive the screens later
 * M6 slices add in its place.
 *
 * Renders a plain div, not its own <main>: since M6.6 it is a child of
 * Shell, which owns the page's single <main> landmark.
 */
function SmokePage() {
  const [health, setHealth] = useState<Health>('idle')

  async function check() {
    setHealth('checking')
    try {
      const res = await fetch('/healthz')
      setHealth(res.ok ? 'online' : 'offline')
    } catch {
      setHealth('offline')
    }
  }

  return (
    <div className="flex justify-center">
      <Card className="w-full max-w-sm shadow-card" size="default">
        <CardHeader>
          <img src="/favicon.svg" alt="" className="size-12" />
          <CardTitle className="text-2xl font-semibold">{copy.smoke.heading}</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          <p className="text-muted-foreground">{copy.smoke.body}</p>
          <Button size="lg" onClick={check} disabled={health === 'checking'}>
            {health === 'checking' ? copy.smoke.checking : copy.smoke.check}
          </Button>
          {health === 'online' && (
            <p className="flex items-center gap-2 text-success">
              <CircleCheck aria-hidden="true" />
              {copy.smoke.online}
            </p>
          )}
          {health === 'offline' && (
            <p className="flex items-center gap-2 text-attention">
              <TriangleAlert aria-hidden="true" />
              {copy.smoke.offline}
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
