import { useState } from 'react'

import { copy } from '@/copy/id'
import { todayISODate } from '@/lib/dates'
import { useApi } from '@/lib/useApi'
import { parseRupiah } from '@/lib/money'
import { createDuesRate, createDuesTier, createMember, postSetup } from '@/lib/setup'
import type { SetupAccountInput } from '@/lib/setup'
import FundName from '@/screens/Setup/FundName'
import Locations from '@/screens/Setup/Locations'
import { newLocationRow } from '@/screens/Setup/locationRow'
import type { LocationRow } from '@/screens/Setup/locationRow'
import OpeningBalances from '@/screens/Setup/OpeningBalances'
import Roster from '@/screens/Setup/Roster'

type Step = 'fund' | 'locations' | 'balances' | 'roster'

/** Local YYYY-MM, same reasoning as lib/dates.ts's todayISODate. */
function currentISOMonth(): string {
  const now = new Date()
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}`
}

/**
 * The first-run setup wizard (PRD section 7.1, issue #138). Four steps; only the
 * fund's name and at least one location are mandatory (the issue's own
 * settled ruling) - opening balances and the roster/tier step are openly
 * optional. This container owns the step state, the data collected so far,
 * and every request; the four step components under this directory are
 * presentational, plus their own local field state.
 *
 * Exactly one request can create the fund (POST /api/setup, fired from the
 * balances step) - #230: a location and its opening balance are born
 * together, in one database transaction, or not at all, so the fund, its
 * accounts and their opening balances all go up in that single call. A
 * failed request keeps her on the balances step with the error rendered, and
 * going back from balances to locations is safe (nothing has posted yet);
 * once the call has succeeded there is no way back to a step that would
 * repeat it (fund_already_exists is a 409) - the roster step has no back.
 *
 * onDone is called once, after the last step (finished or skipped) - App.tsx
 * uses it to re-probe GET /api/fund and move on to home without a reload.
 */
export default function Setup({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState<Step>('fund')
  const [fundName, setFundName] = useState('')
  // Lazy: newLocationRow mints a client id, so the seed rows are built once,
  // not on every render.
  const [locationRows, setLocationRows] = useState<LocationRow[]>(() => [
    newLocationRow('cash', 'Tunai'),
    newLocationRow('bank', 'Bank'),
  ])
  const [tierName, setTierName] = useState('')
  const [rateAmount, setRateAmount] = useState('')
  const [members, setMembers] = useState<string[]>([])

  const [state, run] = useApi<unknown>()
  const submitting = state.status === 'loading'
  const error = state.status === 'error' ? state.error : undefined

  function submitBalances() {
    void run(async () => {
      const occurredOn = todayISODate()
      const accounts: SetupAccountInput[] = locationRows.map((row) => {
        const name = row.name.trim()
        const amount = parseRupiah(row.openingBalance)
        const input: SetupAccountInput = { kind: row.kind, name }
        if (amount > 0) {
          input.opening_balance = { amount, occurred_on: occurredOn, note: copy.setup.balances.note(name) }
        }
        return input
      })
      const result = await postSetup(fundName.trim(), accounts)
      setStep('roster')
      return result
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
        onNext={() => setStep('balances')}
        onBack={() => setStep('fund')}
      />
    )
  }

  if (step === 'balances') {
    return (
      <OpeningBalances
        rows={locationRows}
        onChange={(clientId, value) =>
          setLocationRows((prev) => prev.map((row) => (row.clientId === clientId ? { ...row, openingBalance: value } : row)))
        }
        onNext={submitBalances}
        onBack={() => setStep('locations')}
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
