import { useEffect, useState, type FormEvent } from 'react'
import { Navigate } from 'react-router-dom'

import DateField from '@/components/DateField'
import AccountPicker from '@/components/pickers/AccountPicker'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { listAccounts } from '@/lib/accounts'
import { ApiError } from '@/lib/api'
import { dateBounds } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { getReimbursement, settleReimbursement } from '@/lib/reimbursements'
import { useApi } from '@/lib/useApi'
import type { Account } from '@/lib/accounts'
import type { Reimbursement } from '@/lib/reimbursements'
import { errorText, FormAlert, TALANGAN_PATH, todayISODate } from './shared'

const text = copy.reimbursements

interface SettleData {
  claim: Reimbursement | null
  accounts: Account[]
}

/**
 * Settle a Talangan claim, on its own screen at `/reimbursements/settle?id=`
 * (#368, ADR-032). The amount and purpose come from the claim itself; she
 * picks only which account pays and when.
 *
 * A claim that is unknown, already settled or waived has nothing to settle,
 * so the screen goes back to the tab (replace) - a stale bookmark, or a
 * claim settled from another device.
 */
export default function Settle({ claimId, onDone, onCancel }: { claimId: number; onDone: () => void; onCancel: () => void }) {
  const [dataState, dataRun] = useApi<SettleData>()
  const [submitState, submitRun] = useApi<unknown>()
  const [error, setError] = useState<string | null>(null)

  function load() {
    void dataRun(async () => {
      const [claim, accounts] = await Promise.all([
        getReimbursement(claimId).catch((err: unknown) => {
          if (err instanceof ApiError && err.code === 'not_found') return null
          throw err
        }),
        listAccounts(),
      ])
      return { claim, accounts }
    })
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dataRun, claimId])

  const submitting = submitState.status === 'loading'

  function handleSettle(accountId: number, occurredOn: string) {
    void submitRun(async () => {
      setError(null)
      try {
        await settleReimbursement(claimId, { account_id: accountId, occurred_on: occurredOn })
        onDone()
      } catch (err) {
        const apiErr = err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err))
        setError(errorText(apiErr))
        // eslint-disable-next-line no-console
        console.error('API error', apiErr.code, apiErr.message)
      }
    })
  }

  if (dataState.status === 'idle' || dataState.status === 'loading') return <Loading />
  if (dataState.status === 'error' || !dataState.data) {
    return dataState.error ? <ErrorState error={dataState.error} onRetry={load} /> : null
  }

  const { claim, accounts } = dataState.data
  if (claim === null || claim.settled || claim.waived_on) return <Navigate to={TALANGAN_PATH} replace />

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-xl font-semibold">{text.settle.heading}</h1>
      <FormAlert message={error} />
      <SettleForm accounts={accounts} claimAmount={claim.amount} onSubmit={handleSettle} onCancel={onCancel} submitting={submitting} />
    </div>
  )
}

function SettleForm({
  accounts,
  claimAmount,
  onSubmit,
  onCancel,
  submitting,
}: {
  accounts: Account[]
  claimAmount: number
  onSubmit: (accountId: number, occurredOn: string) => void
  onCancel: () => void
  submitting: boolean
}) {
  const [accountId, setAccountId] = useState<number | null>(null)
  const [occurredOn, setOccurredOn] = useState(todayISODate)

  const canSubmit = accountId !== null && occurredOn !== '' && !submitting

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit || accountId === null) return
    onSubmit(accountId, occurredOn)
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
      <div className="flex items-center justify-between text-sm">
        <span className="text-muted-foreground">{text.record.amountLabel}</span>
        <span className="tabular font-medium">{formatIDR(claimAmount)}</span>
      </div>

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
      </div>

      <div className="grid grid-cols-2 gap-2">
        <Button type="button" size="lg" variant="outline" onClick={onCancel} disabled={submitting}>
          {text.settle.cancel}
        </Button>
        <Button type="submit" size="lg" disabled={!canSubmit}>
          {submitting ? text.settle.submitting : text.settle.submit}
        </Button>
      </div>
    </form>
  )
}
