// Typed calls over apiFetch for the three auth routes M6.4's screens need,
// plus logout (wired, unexposed - see its own comment below). ADR-030's
// gate itself is a Go decision; this module only names its wire shapes on
// the client so App.tsx and the screens don't hand-roll fetch calls.

import { apiFetch } from '@/lib/api'

/** GET /api/session's entire body (internal/http/session.go). Never 401. */
export interface SessionStatus {
  authenticated: boolean
  has_account: boolean
}

/** The user shape POST /api/register and POST /api/login both answer with. */
export interface AuthUser {
  id: number
  email: string
  created_at: number
}

export function getSession(): Promise<SessionStatus> {
  return apiFetch<SessionStatus>('/api/session')
}

// A successful register also establishes the session server-side
// (register.go: RenewToken + Put) - there is no separate "log in after
// registering" step on the client.
export function register(email: string, password: string): Promise<AuthUser> {
  return apiFetch<AuthUser>('/api/register', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  })
}

export function login(email: string, password: string): Promise<AuthUser> {
  return apiFetch<AuthUser>('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  })
}

// Wired but deliberately not called from any screen this slice - a real
// logout affordance belongs with the app shell chrome (M6.6). Exists now so
// the route is proven reachable before a human has anything to click.
export function logout(): Promise<void> {
  return apiFetch<void>('/api/logout', { method: 'POST' })
}
