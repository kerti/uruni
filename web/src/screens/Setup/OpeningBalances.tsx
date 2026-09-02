import type { FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { formatRupiahDigits, parseRupiah } from '@/lib/money'
import type { ApiError } from '@/lib/api'
import type { Account } from '@/lib/setup'

const text = copy.setup

/**
 * Setup step 3 of 4: an optional opening balance per location POST /api/setup
 * just created. Left blank, that account's field never leaves the browser -
 * Setup.tsx only fires a request for a field that parses to a non-zero
 * amount, matching PostOpeningBalance's own "a zero amount posts no row"
 * contract rather than sending a zero-amount request just to have the route
 * tolerate it.
 *
 * Fields hold the grouped display string (formatRupiahDigits), re-derived on
 * every keystroke from parseRupiah so a typed "1000000" always reads back as
 * "1.000.000" - the same edge money.ts documents for any amount input.
 */
export default function OpeningBalances({
  accounts,
  amounts,
  onChange,
  onNext,
  submitting,
  error,
}: {
  accounts: Account[]
  amounts: Record<number, string>
  onChange: (accountId: number, value: string) => void
  onNext: () => void
  submitting: boolean
  error: ApiError | undefined
}) {
  function handleAmountChange(accountId: number, raw: string) {
    const digits = parseRupiah(raw)
    onChange(accountId, digits === 0 ? '' : formatRupiahDigits(digits))
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    onNext()
  }

  return (
    <main className="flex min-h-dvh items-center justify-center p-6">
      <Card className="w-full max-w-sm shadow-card" size="default">
        <CardHeader>
          <p className="text-sm text-muted-foreground">{text.stepLabel(3)}</p>
          <CardTitle className="text-2xl font-semibold">{text.balances.heading}</CardTitle>
          <p className="text-muted-foreground">{text.balances.body}</p>
        </CardHeader>
        <CardContent>
          <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
            {accounts.map((account) => (
              <div key={account.id} className="flex flex-col gap-1.5">
                <Label htmlFor={`setup-balance-${account.id}`}>{text.balances.amountLabel(account.name)}</Label>
                <Input
                  id={`setup-balance-${account.id}`}
                  type="text"
                  inputMode="numeric"
                  value={amounts[account.id] ?? ''}
                  onChange={(event) => handleAmountChange(account.id, event.target.value)}
                  disabled={submitting}
                />
              </div>
            ))}
            {error && <ErrorState error={error} />}
            <Button type="submit" size="lg" disabled={submitting}>
              {submitting ? text.submitting : text.next}
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
