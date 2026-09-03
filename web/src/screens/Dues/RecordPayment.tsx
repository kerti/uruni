import { useEffect, useState, type FormEvent } from 'react'

import AmountInput from '@/components/money/AmountInput'
import AccountPicker from '@/components/pickers/AccountPicker'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { listAccounts } from '@/lib/accounts'
import { createDuesPayment, getOutstandingDues } from '@/lib/dues'
import { formatIDR } from '@/lib/money'
import { listPurposes } from '@/lib/purposes'
import { listMembers } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { OutstandingDuesPeriod } from '@/lib/dues'
import type { Purpose } from '@/lib/purposes'
import type { Member } from '@/lib/setup'

const text = copy.dues.payment

/** Local YYYY-MM-DD / YYYY-MM - never toISOString(), which is UTC and can
 * read a day (and so a month) early in WIB. Same helpers as
 * RecordTransaction.tsx's todayISODate and Status.tsx's currentISOMonth. */
function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

function currentISOMonth(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

const monthFormatter = new Intl.DateTimeFormat('id-ID', { month: 'long', year: 'numeric' })

/** "2026-09" -> "September 2026". Built from the parsed parts rather than
 * `new Date('2026-09')`, which is parsed as UTC midnight and can render as
 * the previous month west of Greenwich. */
function formatPeriod(period: string): string {
  const [year, month] = period.split('-').map(Number)
  if (!Number.isFinite(year) || !Number.isFinite(month)) return period
  return monthFormatter.format(new Date(year, month - 1, 1))
}

/** What one outstanding period still needs to settle it: the tier's
 * effective rate for that month, less anything already paid toward it. For
 * an `unpaid` period that is the whole rate; for a `partial` one it is the
 * sisa, which is what the treasurer is actually collecting - pre-filling the
 * full rate there would overpay the month. */
function remainingOf(period: OutstandingDuesPeriod): number {
  return Math.max(period.owed_amount - period.paid_amount, 0)
}

interface FormData {
  members: Member[]
  accounts: Account[]
  purposes: Purpose[]
}

/**
 * Recording a dues payment (M6.13, PRD §7.3): "mark a dues payment (amount
 * auto-filled from the member's tier, editable; location; cash/transfer),"
 * including the multi-period case.
 *
 * The periods on offer come from GET /api/members/{id}/outstanding-dues,
 * whose rows already carry the effective rate for each month (#186) - this
 * screen never looks a rate up itself, and never offers a period the server
 * did not call outstanding. That is also what keeps it from becoming a way
 * to enter settled history: a fund's history starts at adoption (#187), so
 * money from before that is already inside the opening balance and simply
 * never appears here.
 *
 * Every selected period is posted in ONE POST /api/dues-payments carrying
 * them all - never one call per period. The server writes them inside a
 * single database transaction, so a validation failure on any period leaves
 * nothing posted at all.
 *
 * Purpose is not a field here: dues land on the fund's own `kind: "main"`
 * purpose, the same silent default RecordTransaction.tsx starts from, and
 * PRD §7.3's form is member / amount / location / date. Note is not a field
 * either - the row already says who paid and for which month.
 *
 * Router-agnostic, same contract as every other screen App.tsx mounts:
 * onRecorded and onCancel are the caller's navigation, not links this
 * component owns.
 */
export default function RecordDuesPayment({
  onRecorded,
  onCancel,
}: {
  onRecorded: () => void
  onCancel: () => void
}) {
  const [loadState, loadRun] = useApi<FormData>()
  const [outstandingState, outstandingRun] = useApi<OutstandingDuesPeriod[]>()
  const [submitState, submitRun] = useApi<unknown>()

  const [memberId, setMemberId] = useState<number | null>(null)
  const [accountId, setAccountId] = useState<number | null>(null)
  const [occurredOn, setOccurredOn] = useState(todayISODate)
  // period -> the amount being paid toward it, pre-filled from the server's
  // own numbers and editable per period; `selected` is which of them this
  // submit actually carries.
  const [amounts, setAmounts] = useState<Record<string, number>>({})
  const [selected, setSelected] = useState<string[]>([])

  async function loadFormData(): Promise<FormData> {
    const [members, accounts, purposes] = await Promise.all([listMembers(), listAccounts(), listPurposes()])
    return { members, accounts, purposes }
  }

  useEffect(() => {
    void loadRun(loadFormData)
    // loadRun is a stable useCallback (useApi.ts); this fires once on mount,
    // same shape as RecordTransaction.tsx's own load.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadRun])

  // Default location: the first active account. Deliberately not
  // RecordTransaction's remembered-last-location - dues are usually
  // collected the same way every month, but that memory belongs to the
  // everyday record form, and sharing its key across two screens would let
  // one silently change the other's default.
  useEffect(() => {
    if (loadState.status !== 'success' || !loadState.data) return
    if (accountId !== null) return
    const active = loadState.data.accounts.filter((a) => a.inactive_on === null)
    if (active.length > 0) setAccountId(active[0].id)
    // accountId is this effect's own output; including it would fight the
    // one-time default (same reasoning as RecordTransaction.tsx).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadState.status, loadState.data])

  // Whenever the member changes, re-read what that member owes. `through` is
  // this client's local month, always sent: the server does not share the
  // treasurer's timezone (#186).
  useEffect(() => {
    if (memberId === null) return
    setSelected([])
    setAmounts({})
    void outstandingRun(() => getOutstandingDues(memberId, currentISOMonth()))
    // outstandingRun is a stable useCallback; memberId is the deliberate
    // dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [outstandingRun, memberId])

  // Pre-fill every offered period with what it would take to settle it. Each
  // one stays independently editable from here on - this only runs when a
  // fresh outstanding response lands.
  useEffect(() => {
    if (outstandingState.status !== 'success' || !outstandingState.data) return
    setAmounts(Object.fromEntries(outstandingState.data.map((p) => [p.period, remainingOf(p)])))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [outstandingState.status, outstandingState.data])

  const periods = outstandingState.data ?? []
  const mainPurpose = loadState.data?.purposes.find((p) => p.kind === 'main') ?? null
  const total = selected.reduce((sum, period) => sum + (amounts[period] ?? 0), 0)
  const submitting = submitState.status === 'loading'
  const canSubmit =
    memberId !== null &&
    accountId !== null &&
    mainPurpose !== null &&
    occurredOn !== '' &&
    selected.length > 0 &&
    selected.every((period) => (amounts[period] ?? 0) > 0) &&
    !submitting

  function togglePeriod(period: string, checked: boolean) {
    setSelected((current) => (checked ? [...current, period] : current.filter((p) => p !== period)))
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || memberId === null || accountId === null || mainPurpose === null) return

    const member = loadState.data?.members.find((m) => m.id === memberId)

    void submitRun(async () => {
      const result = await createDuesPayment({
        memberId,
        accountId,
        purposeId: mainPurpose.id,
        occurredOn,
        // Every posted row says what it is and whose it is - a dues payment
        // that reaches recent activity or the report with an empty note
        // reads as a bare amount. Derived, never typed: PRD §7.3's form is
        // member / amount / location / date, and the member is already
        // chosen above.
        note: text.note(member?.name ?? ''),
        // In the order the server offered them - oldest first - so a
        // multi-period payment reads the way it was collected.
        periods: periods
          .filter((p) => selected.includes(p.period))
          .map((p) => ({ dues_period: p.period, amount: amounts[p.period] ?? 0 })),
      })
      onRecorded()
      return result
    })
  }

  if (loadState.status === 'idle' || loadState.status === 'loading') {
    return <Loading />
  }

  if (loadState.status === 'error' || !loadState.data) {
    return loadState.error ? <ErrorState error={loadState.error} onRetry={() => void loadRun(loadFormData)} /> : null
  }

  const selectableMembers = loadState.data.members.filter((m) => m.inactive_on === null)

  return (
    <form className="mx-auto flex w-full max-w-sm flex-col gap-4" onSubmit={handleSubmit} noValidate>
      <h1 className="text-2xl font-semibold">{text.heading}</h1>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="dues-payment-member">{text.memberLabel}</Label>
        <select
          id="dues-payment-member"
          className="h-11 w-full rounded-lg border border-input bg-transparent px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 md:text-sm"
          value={memberId ?? ''}
          onChange={(event) => setMemberId(Number(event.target.value))}
          disabled={submitting || selectableMembers.length === 0}
        >
          <option value="" disabled>
            {text.memberPlaceholder}
          </option>
          {selectableMembers.map((member) => (
            <option key={member.id} value={member.id}>
              {member.name}
            </option>
          ))}
        </select>
      </div>

      <div className="flex flex-col gap-2">
        <h2 className="text-sm font-medium">{text.periodsHeading}</h2>

        {memberId === null && <p className="text-muted-foreground">{text.noMemberYet}</p>}

        {memberId !== null && (outstandingState.status === 'idle' || outstandingState.status === 'loading') && <Loading />}

        {outstandingState.status === 'error' && outstandingState.error && (
          <ErrorState
            error={outstandingState.error}
            onRetry={() => void outstandingRun(() => getOutstandingDues(memberId as number, currentISOMonth()))}
          />
        )}

        {outstandingState.status === 'success' &&
          (periods.length === 0 ? (
            <p className="text-muted-foreground">{text.noOutstanding}</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {periods.map((period) => {
                const checked = selected.includes(period.period)
                return (
                  <li
                    key={period.period}
                    className="flex flex-col gap-2 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10"
                  >
                    <label className="flex items-center gap-2 font-medium">
                      <input
                        type="checkbox"
                        className="size-4 rounded border-input"
                        checked={checked}
                        onChange={(event) => togglePeriod(period.period, event.target.checked)}
                        disabled={submitting}
                      />
                      {formatPeriod(period.period)}
                    </label>

                    {period.status === 'partial' && (
                      <p className="tabular text-sm text-muted-foreground">
                        {copy.dues.paidLabel}: {formatIDR(period.paid_amount)} · {text.remainingLabel}:{' '}
                        {formatIDR(remainingOf(period))}
                      </p>
                    )}

                    <AmountInput
                      id={`dues-payment-amount-${period.period}`}
                      label={text.amountLabel(formatPeriod(period.period))}
                      value={amounts[period.period] ?? 0}
                      onChange={(amount) => setAmounts((current) => ({ ...current, [period.period]: amount }))}
                      disabled={submitting || !checked}
                    />
                  </li>
                )
              })}
            </ul>
          ))}
      </div>

      {selected.length > 0 && (
        <p className="tabular flex items-center justify-between font-medium">
          <span>{text.totalLabel}</span>
          <span>{formatIDR(total)}</span>
        </p>
      )}

      <AccountPicker
        id="dues-payment-account"
        label={text.locationLabel}
        accounts={loadState.data.accounts}
        value={accountId}
        onChange={setAccountId}
        disabled={submitting}
      />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="dues-payment-date">{text.dateLabel}</Label>
        <Input
          id="dues-payment-date"
          type="date"
          className="h-11"
          value={occurredOn}
          onChange={(event) => setOccurredOn(event.target.value)}
          disabled={submitting}
          required
        />
      </div>

      {submitState.status === 'error' && submitState.error && <ErrorState error={submitState.error} />}

      <Button type="submit" size="lg" disabled={!canSubmit}>
        {submitting ? text.submitting : text.submit}
      </Button>
      <Button type="button" variant="outline" size="lg" onClick={onCancel} disabled={submitting}>
        {text.cancel}
      </Button>
    </form>
  )
}
