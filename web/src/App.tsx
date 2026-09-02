import { useEffect, useState } from 'react'
import { CircleCheck, TriangleAlert } from 'lucide-react'
import { BrowserRouter, Route, Routes } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import OfflineBanner from '@/components/states/OfflineBanner'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import Register from '@/screens/Register'
import Login from '@/screens/Login'
import { getSession } from '@/lib/auth'
import { useApi } from '@/lib/useApi'
import type { SessionStatus, AuthUser } from '@/lib/auth'
import { copy } from '@/copy/id'

/**
 * Router root for the whole SPA (M6.2, extended by M6.4's session probe).
 * This shell has a known death date: M6.6 replaces it with the everyday-loop
 * layout, and SmokePage is itself a placeholder M6.5 replaces once setup
 * lands. It exists now so `/healthz` keeps resolving through a real router
 * during review, before any of that lands.
 *
 * On mount it probes GET /api/session once and routes off the result:
 * `has_account: false` renders Register, `has_account: true,
 * authenticated: false` renders Login, and `authenticated: true` renders
 * the (still-placeholder) authenticated branch - M6.5 decides setup-vs-home
 * there from GET /api/fund, not here. A successful register or login calls
 * back into this component and optimistically marks the probe authenticated
 * rather than re-fetching /api/session, so the handoff needs no reload.
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

  return (
    <BrowserRouter>
      <OfflineBanner />
      <Routes>
        <Route
          path="*"
          element={
            <AuthGate state={state} onRetry={() => void run(getSession)} onAuthenticated={handleAuthenticated} />
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
}: {
  state: ReturnType<typeof useApi<SessionStatus>>[0]
  onRetry: () => void
  onAuthenticated: (user: AuthUser) => void
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

  return <SmokePage />
}

type Health = 'idle' | 'checking' | 'online' | 'offline'

/**
 * The M1 smoke page, carried over unpolished as a connectivity check a human
 * can still run by hand. Nothing here is meant to survive the screens later
 * M6 slices add in its place.
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
    <main className="flex min-h-dvh items-center justify-center p-6">
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
    </main>
  )
}
