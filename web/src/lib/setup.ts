// Typed calls over apiFetch for the setup wizard's routes (M6.5), same idiom
// as lib/auth.ts. The wire shapes are copied verbatim from internal/http -
// see setup.go, accounts.go, dues_tiers.go, dues_rates.go and members.go for
// the Go side of each one.

import { apiFetch } from '@/lib/api'

/** An account (location) row - cash or bank - as every route here returns it. */
export interface Account {
  id: number
  kind: 'cash' | 'bank'
  name: string
  inactive_on: string | null
  created_at: number
}

/** GET /api/fund and setupResponse.fund's shape (internal/http/setup.go). */
export interface Fund {
  id: number
  name: string
  currency: string
  report_slug: string
  created_at: number
}

/** POST /api/setup's 201 body. */
export interface SetupResult {
  fund: Fund
  main_purpose_id: number
  accounts: Account[]
}

/** One row of POST /api/setup's `accounts` array. */
export interface SetupAccountInput {
  kind: 'cash' | 'bank'
  name: string
}

/** The posted transaction row a successful opening balance answers with. */
export interface Transaction {
  id: number
  account_id: number
  purpose_id: number
  direction: string
  amount: number
  occurred_on: string
  kind: string
  member_id: number | null
  dues_period: string | null
  reimbursement_id: number | null
  transfer_id: number | null
  reverses_transaction_id: number | null
  note: string | null
  created_at: number
}

/** POST /api/accounts/{id}/opening-balance's body. */
export interface OpeningBalanceResult {
  transaction: Transaction | null
  posted_amount: number
}

export interface DuesTier {
  id: number
  name: string
  created_at: number
}

export interface DuesRate {
  id: number
  tier_id: number
  amount: number
  effective_from: string
  created_at: number
}

export interface Member {
  id: number
  name: string
  tier_id: number | null
  joined_on: string | null
  inactive_on: string | null
  created_at: number
}

/** GET /api/fund - 404 not_found means "run setup". */
export function getFund(): Promise<Fund> {
  return apiFetch<Fund>('/api/fund')
}

/**
 * PATCH /api/fund - renames the kas. The name is a display label: it heads
 * every screen and the public report, and nothing posted references it, so
 * this rewrites no history. currency and report_slug are not settable -
 * one is an invariant through 0.x, the other is the report's unguessable
 * address and rotating it is its own decision.
 */
export function renameFund(name: string): Promise<Fund> {
  return apiFetch<Fund>('/api/fund', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}

/**
 * POST /api/setup - the fund's name plus every account it starts with, in
 * one call. A second call for a fund that already exists is
 * 409 fund_already_exists.
 */
export function postSetup(name: string, accounts: SetupAccountInput[]): Promise<SetupResult> {
  return apiFetch<SetupResult>('/api/setup', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, accounts }),
  })
}

/**
 * POST /api/accounts/{id}/opening-balance. A zero amount still posts no row
 * server-side (PostOpeningBalance's own contract) - callers here only reach
 * this at all for an account the treasurer actually filled in, see
 * Setup.tsx.
 */
export function postOpeningBalance(accountId: number, amount: number, occurredOn: string, note: string): Promise<OpeningBalanceResult> {
  return apiFetch<OpeningBalanceResult>(`/api/accounts/${accountId}/opening-balance`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ amount, occurred_on: occurredOn, note }),
  })
}

export function createDuesTier(name: string): Promise<DuesTier> {
  return apiFetch<DuesTier>('/api/dues-tiers', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}

export function createDuesRate(tierId: number, amount: number, effectiveFrom: string): Promise<DuesRate> {
  return apiFetch<DuesRate>(`/api/dues-tiers/${tierId}/rates`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ amount, effective_from: effectiveFrom }),
  })
}

/** GET /api/members - the fund's whole roster, retired members included
 * (`inactive_on` set). A caller recording new money filters those out the
 * way AccountPicker does for a retired location; a caller reading history
 * needs them. */
export function listMembers(): Promise<Member[]> {
  return apiFetch<Member[]>('/api/members')
}

export function createMember(name: string, tierId: number | null, joinedOn: string | null): Promise<Member> {
  return apiFetch<Member>('/api/members', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, tier_id: tierId, joined_on: joinedOn }),
  })
}
