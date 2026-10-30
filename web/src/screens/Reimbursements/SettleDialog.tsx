import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react'

import DateField from '@/components/DateField'
import OpeningDateHint from '@/components/OpeningDateHint'
import AccountPicker from '@/components/pickers/AccountPicker'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { listAccounts } from '@/lib/accounts'
import { errorCode } from '@/lib/api'
import { dateBounds, formatIsoDate, todayISODate } from '@/lib/dates'
import { defaultSourceAccountId, rememberAccountId } from '@/lib/lastAccount'
import { formatIDR } from '@/lib/money'
import { listPurposes } from '@/lib/purposes'
import { getReimbursement, settleReimbursement } from '@/lib/reimbursements'
import { listAllMembers } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { Reimbursement } from '@/lib/reimbursements'
import { errorText } from './helpers'

const text = copy.reimbursements

interface SettleData {
  /** The id asked for, so data left over from a previous claim is never mistaken for this one's. */
  id: number
  claim: Reimbursement | null
  accounts: Account[]
  memberName: string
  purposeName: string
}

/**
 * Settling a Talangan claim, as a dialog on the tab (ADR-046), addressed by
 * `?edit=settle:<id>`. The amount and pos come from the claim and are shown
 * above the one picker and one date she fills in; she types no money figure.
 *
 * It posts, so a tap outside does nothing (`posts`); the X closes it, and an
 * edit dialog carries no cancel button of its own (#742).
 *
 * It reads the claim itself rather than borrowing the list's row: a deep link,
 * or a claim on a page the list has not loaded, still opens. A claim that is
 * unknown, already settled or waived has nothing to settle, so the dialog never
 * opens and `onUnusable` lets the caller strip the param - a stale bookmark, or
 * a claim settled from another device. `claimId` is null while closed.
 */
export default function SettleDialog({
  claimId,
  onClose,
  onSettled,
  onUnusable,
}: {
  claimId: number | null
  onClose: () => void
  onSettled: () => void
  onUnusable: () => void
}) {
  const [dataState, dataRun] = useApi<SettleData>()
  const load = useCallback(
    (id: number, silent = false) =>
      dataRun(
        async () => {
          const [claim, accounts, members, purposes] = await Promise.all([
            getReimbursement(id).catch((err: unknown) => {
              if (errorCode(err) === 'not_found') return null
              throw err
            }),
            listAccounts(),
            listAllMembers(),
            listPurposes(),
          ])
          // Names, not ids: she opens this to pay someone back for something,
          // and should read who and what before she picks a source.
          const memberName = members.find((m) => m.id === claim?.member_id)?.name ?? ''
          const purposeName = purposes.find((p) => p.id === claim?.purpose_id)?.name ?? ''
          return { id, claim, accounts, memberName, purposeName }
        },
        { silent },
      ),
    [dataRun],
  )

  useEffect(() => {
    if (claimId !== null) void load(claimId)
  }, [claimId, load])

  const data = dataState.data
  const current = dataState.status === 'success' && data !== undefined && data.id === claimId
  const unusable = current && (data.claim === null || data.claim.settled || !!data.claim.waived_on)
  useEffect(() => {
    if (claimId !== null && unusable) onUnusable()
  }, [claimId, unusable, onUnusable])

  // Not open until the claim has loaded and is payable: a flash of an empty
  // dialog that then vanishes is worse than a beat of nothing. A failed load
  // does open, to say so.
  const usable = current && data.claim !== null && !unusable
  const open = claimId !== null && (usable || dataState.status === 'error')

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close} posts>
        <DialogHeader>
          <DialogTitle>{text.settle.heading}</DialogTitle>
        </DialogHeader>
        {dataState.status === 'error' && dataState.error && (
          <ErrorState error={dataState.error} onRetry={claimId === null ? undefined : () => void load(claimId)} />
        )}
        {dataState.status === 'loading' && <Loading />}
        {usable && data && data.claim && (
          <SettleForm
            accounts={data.accounts}
            claim={data.claim}
            memberName={data.memberName}
            purposeName={data.purposeName}
            // A lost response may still have saved; re-read the claim before she can retry.
            reload={() => load(data.id, true)}
            onSettled={onSettled}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

function SettleForm({
  accounts,
  claim,
  memberName,
  purposeName,
  reload,
  onSettled,
}: {
  accounts: Account[]
  claim: Reimbursement
  memberName: string
  purposeName: string
  reload: () => Promise<void>
  onSettled: () => void
}) {
  // Here, not in the dialog: the form is mounted per open, so a failed
  // attempt's message never greets the next one.
  const [submitState, submitRun] = useApi<unknown>()
  const submitting = submitState.status === 'loading'
  const error = submitState.status === 'error' && submitState.error ? errorText(submitState.error) : null

  // Blank until she picks: until then, the same default every source picker
  // has (#737).
  const [pickedAccountId, setAccountId] = useState<number | null>(null)
  const accountId = pickedAccountId ?? defaultSourceAccountId(accounts)
  const [occurredOn, setOccurredOn] = useState(todayISODate)

  const canSubmit = accountId !== null && occurredOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || accountId === null) return
    void submitRun(
      async () => {
        await settleReimbursement(claim.id, { account_id: accountId, occurred_on: occurredOn })
        rememberAccountId(accountId)
        onSettled()
      },
      { settle: reload },
    )
  }

  return (
    <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
      <dl className="flex flex-col gap-2 text-sm">
        <SummaryRow label={text.record.memberLabel} value={memberName} />
        <SummaryRow label={text.record.purposeLabel} value={purposeName} />
        <SummaryRow label={text.record.dateLabel} value={formatIsoDate(claim.incurred_on)} />
        {claim.note && <SummaryRow label={copy.transactionDetail.noteLabel} value={claim.note} />}
        <SummaryRow label={text.record.amountLabel} value={<span className="tabular font-medium">{formatIDR(claim.amount)}</span>} />
      </dl>

      <AccountPicker
        id="settle-account"
        label={text.settle.accountLabel}
        accounts={accounts}
        value={accountId}
        onChange={setAccountId}
        disabled={submitting}
      />

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="settle-date">{text.settle.dateLabel}</Label>
        <DateField id="settle-date" value={occurredOn} onChange={setOccurredOn} bounds={dateBounds.entry()} disabled={submitting} />
        <OpeningDateHint date={occurredOn} accounts={accounts} accountIds={[accountId]} />
      </div>

      {error && (
        <p role="alert" className="text-sm text-attention-text">
          {error}
        </p>
      )}

      <DialogFooter className="mt-1">
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.settle.submitting : text.settle.submit}
        </Button>
      </DialogFooter>
    </form>
  )
}

function SummaryRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <dt className="text-muted-foreground shrink-0">{label}</dt>
      <dd className="text-right break-words">{value}</dd>
    </div>
  )
}
