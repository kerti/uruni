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

/**
 * PATCH /api/members/{id}. The server distinguishes three cases per field
 * and so must this: an **absent key** means "leave alone", an explicit
 * **null** means "clear it" (no tier, or reinstated), and a value sets it.
 * A partial object cannot express that on its own - `{ tierId: null }` and
 * `{}` are indistinguishable once spread - so each field is passed as
 * `undefined` for "leave alone" and the body is built key by key below.
 *
 * Clearing `tier_id` drops the member's dues obligation; clearing
 * `inactive_on` reinstates them.
 */
export interface MemberPatch {
  name?: string
  tierId?: number | null
  joinedOn?: string | null
  inactiveOn?: string | null
}

export function updateMember(id: number, patch: MemberPatch): Promise<Member> {
  const body: Record<string, unknown> = {}
  if (patch.name !== undefined) body.name = patch.name
  if (patch.tierId !== undefined) body.tier_id = patch.tierId
  if (patch.joinedOn !== undefined) body.joined_on = patch.joinedOn
  if (patch.inactiveOn !== undefined) body.inactive_on = patch.inactiveOn
  return apiFetch<Member>(`/api/members/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

/**
 * DELETE /api/members/{id} - only for a duplicate with no posted history.
 * A member any transaction references is refused by the foreign key as 409
 * `referenced_by_other_records`, which the roster renders as "sudah punya
 * riwayat - nonaktifkan, bukan hapus": deactivating is what she wants for
 * someone who actually left.
 */
export function deleteMember(id: number): Promise<void> {
  return apiFetch<void>(`/api/members/${id}`, { method: 'DELETE' })
}

/** GET /api/dues-tiers - the fund's tiers, for choosing a member's. */
export function listDuesTiers(): Promise<DuesTier[]> {
  return apiFetch<DuesTier[]>('/api/dues-tiers')
}

/** PATCH /api/dues-tiers/{id} - renames a tier. The name is a label; a
 * member references the tier by id. A duplicate name for the fund hits the
 * schema's UNIQUE (fund_id, name) and comes back 409. */
export function renameDuesTier(id: number, name: string): Promise<DuesTier> {
  return apiFetch<DuesTier>(`/api/dues-tiers/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
}

/** GET /api/dues-tiers/{id}/rates - a tier's rate history, oldest first. An
 * empty list is a legal state: a tier whose price is not decided yet. */
export function listDuesRates(tierId: number): Promise<DuesRate[]> {
  return apiFetch<DuesRate[]>(`/api/dues-tiers/${tierId}/rates`)
}

/** PATCH /api/dues-rates/{id} - corrects a mistyped amount on a rate just
 * entered. Never a price change: a new price is a new row for a new month
 * (createDuesRate above), because the old one still explains old periods. */
export function updateDuesRate(id: number, amount: number): Promise<DuesRate> {
  return apiFetch<DuesRate>(`/api/dues-rates/${id}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ amount }),
  })
}

/** DELETE /api/dues-rates/{id} - for a rate filed against the wrong month,
 * which is what makes that correctable at all: UNIQUE (tier_id,
 * effective_from) refuses the corrected row while the wrong one stands. */
export function deleteDuesRate(id: number): Promise<void> {
  return apiFetch<void>(`/api/dues-rates/${id}`, { method: 'DELETE' })
}
