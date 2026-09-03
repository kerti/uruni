import { useRef, useState } from 'react'

import { copy } from '@/copy/id'
import { useApi } from '@/lib/useApi'
import { parseRupiah } from '@/lib/money'
import { createDuesRate, createDuesTier, createMember, postOpeningBalance, postSetup } from '@/lib/setup'
import type { SetupAccountInput, SetupResult } from '@/lib/setup'
import FundName from '@/screens/Setup/FundName'
import Locations from '@/screens/Setup/Locations'
import OpeningBalances from '@/screens/Setup/OpeningBalances'
import Roster from '@/screens/Setup/Roster'

type Step = 'fund' | 'locations' | 'balances' | 'roster'

/** Local YYYY-MM-DD - never toISOString(), which is UTC and can read as
 * yesterday's date in WIB. */
function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

/** Local YYYY-MM, same reasoning as todayISODate. */
function currentISOMonth(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

/**
 * The first-run setup wizard (PRD §7.1, issue #138). Four steps; only the
 * fund's name and at least one location are mandatory (the issue's own
 * settled ruling) - opening balances and the roster/tier step are openly
 * optional. This container owns the step state, the data collected so far,
 * and every request; the four step components under this directory are
 * presentational, plus their own local field state.
 *
 * Exactly one request can create the fund (POST /api/setup, in the
 * locations step) - a failed request anywhere keeps her on the step she is
 * on with the error rendered, and once that call has succeeded there is no
 * way back to a step that would repeat it (fund_already_exists is a 409).
 *
 * onDone is called once, after the last step (finished or skipped) - App.tsx
 * uses it to re-probe GET /api/fund and move on to home without a reload.
 */
export default function Setup({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState<Step>('fund')
  const [fundName, setFundName] = useState('')
  const [locationRows, setLocationRows] = useState<SetupAccountInput[]>([
    { kind: 'cash', name: 'Tunai' },
    { kind: 'bank', name: 'Bank' },
  ])
  const [setupResult, setSetupResult] = useState<SetupResult | undefined>(undefined)
  const [balanceAmounts, setBalanceAmounts] = useState<Record<number, string>>({})
  // Accounts an opening balance has already been posted for - a ref, not
  // state: it only ever guards a retry after a partial failure, never drives
  // a render, and must survive across submitBalances calls without racing
  // its own setState.
  const postedAccountIds = useRef<Set<number>>(new Set())
  const [tierName, setTierName] = useState('')
  const [rateAmount, setRateAmount] = useState('')
  const [members, setMembers] = useState<string[]>([])

  const [state, run] = useApi<unknown>()
  const submitting = state.status === 'loading'
  const error = state.status === 'error' ? state.error : undefined

  function submitLocations() {
    void run(async () => {
      const result = await postSetup(
        fundName.trim(),
        locationRows.map((row) => ({ kind: row.kind, name: row.name.trim() })),
      )
      setSetupResult(result)
      setStep('balances')
      return result
    })
  }

  function submitBalances() {
    void run(async () => {
      const result = setupResult
      if (!result) return
      const occurredOn = todayISODate()
      for (const account of result.accounts) {
        if (postedAccountIds.current.has(account.id)) continue
        const amount = parseRupiah(balanceAmounts[account.id] ?? '')
        if (amount === 0) continue
        await postOpeningBalance(account.id, amount, occurredOn, copy.setup.balances.note(account.name))
        postedAccountIds.current.add(account.id)
      }
      setStep('roster')
    })
  }

  function submitRoster() {
    void run(async () => {
      const trimmedTierName = tierName.trim()
      const filledMembers = members.map((name) => name.trim()).filter((name) => name !== '')

      let tierId: number | null = null
      if (trimmedTierName !== '') {
        const tier = await createDuesTier(trimmedTierName)
        tierId = tier.id
        const rate = parseRupiah(rateAmount)
        if (rate > 0) {
          await createDuesRate(tier.id, rate, currentISOMonth())
        }
      }

      const joinedOn = todayISODate()
      for (const name of filledMembers) {
        await createMember(name, tierId, joinedOn)
      }

      onDone()
    })
  }

  if (step === 'fund') {
    return <FundName name={fundName} onChange={setFundName} onNext={() => setStep('locations')} />
  }

  if (step === 'locations') {
    return (
      <Locations
        rows={locationRows}
        onChange={setLocationRows}
        onNext={submitLocations}
        onBack={() => setStep('fund')}
        submitting={submitting}
        error={error}
      />
    )
  }

  if (step === 'balances' && setupResult) {
    return (
      <OpeningBalances
        accounts={setupResult.accounts}
        amounts={balanceAmounts}
        onChange={(accountId, value) => setBalanceAmounts((prev) => ({ ...prev, [accountId]: value }))}
        onNext={submitBalances}
        submitting={submitting}
        error={error}
      />
    )
  }

  return (
    <Roster
      tierName={tierName}
      onTierNameChange={setTierName}
      rateAmount={rateAmount}
      onRateAmountChange={setRateAmount}
      members={members}
      onMembersChange={setMembers}
      onSkip={onDone}
      onFinish={submitRoster}
      submitting={submitting}
      error={error}
    />
  )
}
