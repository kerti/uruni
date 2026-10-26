// Typed calls over apiFetch for the auth routes, plus logout and the
// treasurer's own login (GET/POST /api/user..., internal/http/user.go).
// ADR-030's gate is a Go decision; this module only names its wire shapes on
// the client.

import { apiFetch, plainConnectionError } from '@/lib/api'

/** GET /api/session's entire body (internal/http/session.go). Never 401. */
export interface SessionStatus {
  authenticated: boolean
  has_account: boolean
  /** The public demo's shared login (ADR-045): both present under URUNI_DEMO,
   * both absent everywhere else. */
  demo_email?: string
  demo_password?: string
}

/** The demo login a probe carries, or null on every real instance. */
export interface DemoLogin {
  email: string
  password: string
}

export function demoLoginOf(session: SessionStatus): DemoLogin | null {
  return session.demo_email && session.demo_password ? { email: session.demo_email, password: session.demo_password } : null
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
// (register.go: RenewToken + Put) - no separate login step on the client.
export function register(email: string, password: string): Promise<AuthUser> {
  return apiFetch<AuthUser>('/api/register', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  }).catch(plainConnectionError)
}

export function login(email: string, password: string): Promise<AuthUser> {
  return apiFetch<AuthUser>('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  }).catch(plainConnectionError)
}

// The shell's logout button (components/Shell.tsx) is the one caller.
export function logout(): Promise<void> {
  return apiFetch<void>('/api/logout', { method: 'POST' }).catch(plainConnectionError)
}

/** The signed-in login, for Pengaturan's Akun bendahara card. Behind the gate:
 * GET /api/session deliberately carries no address. */
export function getUser(): Promise<AuthUser> {
  return apiFetch<AuthUser>('/api/user')
}

// Both ask for the CURRENT password and sign every other device out; this
// session stays. A wrong one answers wrong_current_password, and counts
// toward a rate limit shared by the two calls. Like login, not a ledger write,
// so a lost response reads as the plain connection message.
export function changeEmail(email: string, currentPassword: string): Promise<AuthUser> {
  return apiFetch<AuthUser>('/api/user/email', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, current_password: currentPassword }),
  }).catch(plainConnectionError)
}

export function changePassword(newPassword: string, currentPassword: string): Promise<void> {
  return apiFetch<void>('/api/user/password', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ new_password: newPassword, current_password: currentPassword }),
  }).catch(plainConnectionError)
}
