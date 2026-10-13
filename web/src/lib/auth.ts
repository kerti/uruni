// Typed calls over apiFetch for the auth routes, plus logout. ADR-030's gate
// is a Go decision; this module only names its wire shapes on the client.

import { apiFetch, plainConnectionError } from '@/lib/api'

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
