import { afterEach, describe, expect, it, vi } from 'vitest'

import accountsSample from '@/test/wire/accounts.json'
import balancesSample from '@/test/wire/balances.json'
import duesPaymentsSample from '@/test/wire/dues-payments-page.json'
import duesGridSample from '@/test/wire/dues-status-grid.json'
import duesStatusSample from '@/test/wire/dues-status.json'
import duesTiersSample from '@/test/wire/dues-tiers.json'
import errorSample from '@/test/wire/error.json'
import fundSample from '@/test/wire/fund.json'
import incidentalSample from '@/test/wire/incidental.json'
import participationSample from '@/test/wire/incidental-participation.json'
import incidentalsSample from '@/test/wire/incidentals.json'
import membersSample from '@/test/wire/members-page.json'
import outstandingSample from '@/test/wire/outstanding-dues.json'
import purposesSample from '@/test/wire/purposes.json'
import latestSample from '@/test/wire/reconciliation-latest.json'
import openLinesSample from '@/test/wire/reconciliation-open-lines.json'
import reconciliationSample from '@/test/wire/reconciliation.json'
import reconciliationsSample from '@/test/wire/reconciliations-page.json'
import reimbursementsSample from '@/test/wire/reimbursements-page.json'
import sessionSample from '@/test/wire/session.json'
import setupSample from '@/test/wire/setup.json'
import transactionsSample from '@/test/wire/transactions-page.json'
import transferSample from '@/test/wire/transfer.json'
import userSample from '@/test/wire/user.json'
import { ApiError, apiFetch } from '@/lib/api'
import { listDuesPayments } from '@/lib/dues'
import { listReconciliations } from '@/lib/reconciliations'
import { listReimbursements } from '@/lib/reimbursements'
import { listMembersPage } from '@/lib/setup'
import { listTransactions } from '@/lib/transactions'
import type { AuthUser, SessionStatus } from '@/lib/auth'
import type { AccountBalance, Balances, PurposeBalance } from '@/lib/balances'
import type { DuesGrid, DuesGridMember, DuesPaymentHistoryRow, DuesStatusRow, OutstandingDuesPeriod } from '@/lib/dues'
import type {
  Incidental,
  IncidentalDetail,
  IncidentalParticipation,
  IncidentalRecipient,
  Participant,
  ParticipationState,
  UnexpectedContribution,
} from '@/lib/incidentals'
import type { Purpose } from '@/lib/purposes'
import type {
  LatestReconciliation,
  OpenReconciliationLine,
  ReconciliationDetail,
  ReconciliationLine,
  ReconciliationListItem,
} from '@/lib/reconciliations'
import type { Reimbursement } from '@/lib/reimbursements'
import type { Account, DuesRate, DuesTierWithRates, Fund, Member, SetupResult, Transaction } from '@/lib/setup'
import type { Transfer } from '@/lib/transfers'

// The web app's half of the wire contract (#524). Each file under
// src/test/wire is a real response, written by internal/http's
// TestWireSamples from a fixed fixture; this file holds every interface to it.
//
// A key list below is a Record over the interface's keys, so tsc keeps it
// exactly equal to the interface. At run time each sample may carry only keys
// its interface declares: a Go field added or renamed without the web side
// following fails here. `satisfies Has<T>` makes tsc refuse a sample missing a
// key the interface requires: a Go field dropped or renamed fails there.
//
// Regenerate the samples with
//   go test ./internal/http -run 'Wire|ErrorCodes' -update

/** The keys of T that are not optional. */
type RequiredKeys<T> = { [K in keyof T]-?: object extends Pick<T, K> ? never : K }[keyof T]
/** Anything that carries every key T requires. Only names, not value types:
 * a JSON import types `"in"` as string, never as the literal union. */
type Has<T> = { [K in RequiredKeys<T>]: unknown }
type Keys<T> = Record<keyof T, true>

/** Asserts every key of each item is one the interface declares. */
function expectDeclared(items: readonly object[], declared: Keys<object>, what: string) {
  expect(items.length, `${what}: the sample has no rows to check`).toBeGreaterThan(0)
  const known = new Set(Object.keys(declared))
  for (const item of items) {
    expect(
      Object.keys(item).filter((key) => !known.has(key)),
      `${what} has keys its interface does not declare`,
    ).toEqual([])
  }
}

const fundKeys: Keys<Fund> = {
  id: true,
  name: true,
  currency: true,
  time_zone: true,
  report_slug: true,
  report_url: true,
  created_at: true,
}
const accountKeys: Keys<Account> = { id: true, kind: true, name: true, inactive_on: true, opening_on: true, created_at: true }
const purposeKeys: Keys<Purpose> = { id: true, kind: true, name: true, created_at: true }
const transactionKeys: Keys<Transaction> = {
  id: true,
  account_id: true,
  purpose_id: true,
  direction: true,
  amount: true,
  occurred_on: true,
  kind: true,
  member_id: true,
  dues_period: true,
  reimbursement_id: true,
  transfer_id: true,
  reverses_transaction_id: true,
  note: true,
  created_at: true,
  account_name: true,
  purpose_kind: true,
  member_name: true,
  claim_note: true,
  transfer_kind: true,
  transfer_from_name: true,
  transfer_to_name: true,
  transfer_corrects_transaction_id: true,
  transfer_reason: true,
  effective_purpose_id: true,
  reversed: true,
  reversible: true,
  counted: true,
  reverses_occurred_on: true,
  receipt_ids: true,
}
const memberKeys: Keys<Member> = {
  id: true,
  name: true,
  tier_id: true,
  joined_on: true,
  inactive_on: true,
  created_at: true,
  tier_name: true,
  current_rate: true,
  arrears_months: true,
}
const duesTierKeys: Keys<DuesTierWithRates> = { id: true, name: true, created_at: true, rates: true }
const duesRateKeys: Keys<DuesRate> = { id: true, tier_id: true, amount: true, effective_from: true, created_at: true }
const setupKeys: Keys<SetupResult> = { fund: true, main_purpose_id: true, accounts: true }
const balancesKeys: Keys<Balances> = { fund_total: true, accounts: true, purposes: true, owed_to_members: true, month: true }
const accountBalanceKeys: Keys<AccountBalance> = { id: true, kind: true, name: true, balance: true }
const purposeBalanceKeys: Keys<PurposeBalance> = { id: true, kind: true, name: true, balance: true }
const incidentalKeys: Keys<Incidental> = {
  purpose_id: true,
  occasion: true,
  target_amount: true,
  opened_on: true,
  closed_on: true,
  created_at: true,
  minimum_per_member: true,
}
const incidentalDetailKeys: Keys<IncidentalDetail> = {
  ...incidentalKeys,
  collected_amount: true,
  disbursed_amount: true,
  balance_amount: true,
  recipients: true,
}
const recipientKeys: Keys<IncidentalRecipient> = { member_id: true, member_name: true }
const participationKeys: Keys<IncidentalParticipation> = { expected: true, unexpected: true }
const participationStateKeys: Keys<ParticipationState> = { member: true, contributed_amount: true, state: true }
const unexpectedKeys: Keys<UnexpectedContribution> = { member: true, contributed_amount: true }
const participantKeys: Keys<Participant> = { id: true, name: true, joined_on: true, inactive_on: true, created_at: true }
const reimbursementKeys: Keys<Reimbursement> = {
  id: true,
  member_id: true,
  purpose_id: true,
  amount: true,
  incurred_on: true,
  waived_on: true,
  settled: true,
  note: true,
  created_at: true,
  receipt_ids: true,
}
const lineKeys: Keys<ReconciliationLine> = {
  id: true,
  account_id: true,
  recorded_amount: true,
  actual_amount: true,
  difference_amount: true,
  resolution: true,
  adjustment_transaction_id: true,
  remainder_amount: true,
}
const openLineKeys: Keys<OpenReconciliationLine> = { ...lineKeys, reconciliation_id: true }
const latestKeys: Keys<LatestReconciliation> = {
  id: true,
  performed_at: true,
  through_transaction_id: true,
  note: true,
  created_at: true,
  has_new_entries: true,
}
const detailKeys: Keys<ReconciliationDetail> = { ...latestKeys, lines: true }
const listItemKeys: Keys<ReconciliationListItem> = { ...latestKeys, open_difference_amount: true }
const duesStatusKeys: Keys<DuesStatusRow> = { member: true, owed_amount: true, paid_amount: true, status: true, paid_through: true }
const duesGridKeys: Keys<DuesGrid> = { periods: true, members: true }
const duesGridMemberKeys: Keys<DuesGridMember> = { id: true, name: true, cells: true }
const outstandingKeys: Keys<OutstandingDuesPeriod> = { period: true, owed_amount: true, paid_amount: true, status: true }
const duesPaymentKeys: Keys<DuesPaymentHistoryRow> = {
  id: true,
  is_reversal: true,
  member_id: true,
  member_name: true,
  dues_period: true,
  amount: true,
  occurred_on: true,
  account_name: true,
  note: true,
  reverses_transaction_id: true,
  reversed_by_transaction_id: true,
  reverses_occurred_on: true,
}
const transferKeys: Keys<Transfer> = { id: true, kind: true, created_at: true }
const sessionKeys: Keys<SessionStatus> = { authenticated: true, has_account: true, demo_email: true, demo_password: true }
const userKeys: Keys<AuthUser> = { id: true, email: true, created_at: true }

// tsc: every sample carries every key its interface requires.
void (fundSample satisfies Has<Fund>)
void (accountsSample satisfies Has<Account>[])
void (purposesSample satisfies Has<Purpose>[])
void (transactionsSample.transactions satisfies Has<Transaction>[])
void (membersSample.members satisfies Has<Member>[])
void (duesTiersSample satisfies Has<DuesTierWithRates>[])
void (duesTiersSample.flatMap((t) => t.rates) satisfies Has<DuesRate>[])
void (setupSample satisfies Has<SetupResult>)
void (balancesSample satisfies Has<Balances>)
void (balancesSample.accounts satisfies Has<AccountBalance>[])
void (balancesSample.purposes satisfies Has<PurposeBalance>[])
void (incidentalsSample satisfies Has<Incidental>[])
void (incidentalSample satisfies Has<IncidentalDetail>)
void (incidentalSample.recipients satisfies Has<IncidentalRecipient>[])
void (participationSample.expected satisfies Has<ParticipationState>[])
void (participationSample.expected.map((row) => row.member) satisfies Has<Participant>[])
void (reimbursementsSample.reimbursements satisfies Has<Reimbursement>[])
void (reconciliationSample satisfies Has<ReconciliationDetail>)
void (reconciliationSample.lines satisfies Has<ReconciliationLine>[])
void (openLinesSample satisfies Has<OpenReconciliationLine>[])
void (latestSample satisfies Has<LatestReconciliation>)
void (reconciliationsSample.reconciliations satisfies Has<ReconciliationListItem>[])
void (duesStatusSample satisfies Has<DuesStatusRow>[])
void (duesStatusSample.map((row) => row.member) satisfies Has<Member>[])
void (duesGridSample satisfies Has<DuesGrid>)
void (duesGridSample.members satisfies Has<DuesGridMember>[])
void (outstandingSample satisfies Has<OutstandingDuesPeriod>[])
void (duesPaymentsSample.dues_payments satisfies Has<DuesPaymentHistoryRow>[])
void (transferSample satisfies Has<Transfer>)
void (sessionSample satisfies Has<SessionStatus>)
void (userSample satisfies Has<AuthUser>)

describe('the wire samples carry no key their interface does not declare', () => {
  const cases: [string, readonly object[], Keys<object>][] = [
    ['Fund', [fundSample, setupSample.fund], fundKeys],
    ['Account', [...accountsSample, ...setupSample.accounts], accountKeys],
    ['Purpose', purposesSample, purposeKeys],
    ['Transaction', transactionsSample.transactions, transactionKeys],
    ['Member', [...membersSample.members, ...duesStatusSample.map((row) => row.member)], memberKeys],
    ['DuesTierWithRates', duesTiersSample, duesTierKeys],
    ['DuesRate', duesTiersSample.flatMap((t) => t.rates), duesRateKeys],
    ['SetupResult', [setupSample], setupKeys],
    ['Balances', [balancesSample], balancesKeys],
    ['AccountBalance', balancesSample.accounts, accountBalanceKeys],
    ['PurposeBalance', balancesSample.purposes, purposeBalanceKeys],
    ['Incidental', incidentalsSample, incidentalKeys],
    ['IncidentalDetail', [incidentalSample], incidentalDetailKeys],
    ['IncidentalRecipient', incidentalSample.recipients, recipientKeys],
    ['IncidentalParticipation', [participationSample], participationKeys],
    ['ParticipationState', participationSample.expected, participationStateKeys],
    ['Participant', participationSample.expected.map((row) => row.member), participantKeys],
    ['Reimbursement', reimbursementsSample.reimbursements, reimbursementKeys],
    ['ReconciliationDetail', [reconciliationSample], detailKeys],
    ['ReconciliationLine', reconciliationSample.lines, lineKeys],
    ['OpenReconciliationLine', openLinesSample, openLineKeys],
    ['LatestReconciliation', [latestSample], latestKeys],
    ['ReconciliationListItem', reconciliationsSample.reconciliations, listItemKeys],
    ['DuesStatusRow', duesStatusSample, duesStatusKeys],
    ['DuesGrid', [duesGridSample], duesGridKeys],
    ['DuesGridMember', duesGridSample.members, duesGridMemberKeys],
    ['OutstandingDuesPeriod', outstandingSample, outstandingKeys],
    ['DuesPaymentHistoryRow', duesPaymentsSample.dues_payments, duesPaymentKeys],
    ['Transfer', [transferSample], transferKeys],
    ['SessionStatus', [sessionSample], sessionKeys],
    ['AuthUser', [userSample], userKeys],
  ]
  it.each(cases)('%s', (what, items, declared) => expectDeclared(items, declared, what))

  // An interface whose every listed row was empty would pass by checking
  // nothing; expectDeclared refuses that, and these name the gaps the fixture
  // leaves on purpose.
  it('UnexpectedContribution: the fixture has none, so only its keys are typed', () => {
    expect(participationSample.unexpected).toEqual([])
    expect(Object.keys(unexpectedKeys)).toEqual(['member', 'contributed_amount'])
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

/** The page routes are read through a function that renames next_cursor;
 * each is run against its sample with a cursor set, so a renamed key on either
 * side leaves nextCursor undefined. */
describe('the page readers find their rows and cursor in the wire page', () => {
  function serve(sample: object) {
    // The cursor goes on the key the sample has: if the server renamed it, the
    // reader's nextCursor comes back undefined.
    const body = 'next_cursor' in sample ? { ...sample, next_cursor: 'c2' } : sample
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } }))),
    )
  }

  it('transactions', async () => {
    serve(transactionsSample)
    const page = await listTransactions({})
    expect(page.transactions).toHaveLength(transactionsSample.transactions.length)
    expect(page.nextCursor).toBe('c2')
  })
  it('members', async () => {
    serve(membersSample)
    const page = await listMembersPage()
    expect(page.members).toHaveLength(membersSample.members.length)
    expect(page.nextCursor).toBe('c2')
  })
  it('dues payments', async () => {
    serve(duesPaymentsSample)
    const page = await listDuesPayments({})
    expect(page.duesPayments).toHaveLength(duesPaymentsSample.dues_payments.length)
    expect(page.nextCursor).toBe('c2')
  })
  it('reimbursements', async () => {
    serve(reimbursementsSample)
    const page = await listReimbursements({})
    expect(page.reimbursements).toHaveLength(reimbursementsSample.reimbursements.length)
    expect(page.nextCursor).toBe('c2')
  })
  it('reconciliations', async () => {
    serve(reconciliationsSample)
    const page = await listReconciliations()
    expect(page.reconciliations).toHaveLength(reconciliationsSample.reconciliations.length)
    expect(page.nextCursor).toBe('c2')
  })
})

describe('the error envelope', () => {
  it('reads code and message, and the field invalid_argument names', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() =>
        Promise.resolve(new Response(JSON.stringify(errorSample), { status: 400, headers: { 'Content-Type': 'application/json' } })),
      ),
    )
    const err = await apiFetch('/api/pass-through-purposes').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).code).toBe(errorSample.error.code)
    expect((err as ApiError).message).toBe(errorSample.error.message)
    expect(errorSample.error.field).toBe('nama')
  })
})
