import type { FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { formatRupiahDigits, parseRupiah } from '@/lib/money'
import type { ApiError } from '@/lib/api'
import type { LocationRow } from '@/screens/Setup/locationRow'

const text = copy.setup

/**
 * Setup step 3 of 4: an optional opening balance per location named on step
 * 2. Left blank, that row's amount never leaves the browser - submitting
 * fires the wizard's one POST /api/setup (#230: the fund, its accounts and
 * their opening balances are created together, in one database transaction,
 * or not at all), and only a row that parsed to a non-zero amount carries an
 * `opening_balance` in that request, matching the server's own "a zero
 * amount posts no row" contract rather than sending a zero-amount balance
 * just to have the route tolerate it.
 *
 * Fields hold the grouped display string (formatRupiahDigits), re-derived on
 * every keystroke from parseRupiah so a typed "1000000" always reads back as
 * "1.000.000" - the same edge money.ts documents for any amount input.
 *
 * Going back to step 2 is always safe: nothing has posted yet, and rows are
 * matched by `clientId` so their names and amounts survive the round trip.
 */
export default function OpeningBalances({
  rows,
  onChange,
  onNext,
  onBack,
  submitting,
  error,
}: {
  rows: LocationRow[]
  onChange: (clientId: string, value: string) => void
  onNext: () => void
  onBack: () => void
  submitting: boolean
  error: ApiError | undefined
}) {
  function handleAmountChange(clientId: string, raw: string) {
    const digits = parseRupiah(raw)
    onChange(clientId, digits === 0 ? '' : formatRupiahDigits(digits))
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
            {rows.map((row) => (
              <div key={row.clientId} className="flex flex-col gap-1.5">
                <Label htmlFor={`setup-balance-${row.clientId}`}>{text.balances.amountLabel(row.name)}</Label>
                <Input
                  id={`setup-balance-${row.clientId}`}
                  type="text"
                  inputMode="numeric"
                  value={row.openingBalance}
                  onChange={(event) => handleAmountChange(row.clientId, event.target.value)}
                  disabled={submitting}
                />
              </div>
            ))}
            {error && <ErrorState error={error} />}
            <div className="flex gap-2">
              <Button type="button" variant="outline" size="lg" onClick={onBack} disabled={submitting}>
                {text.back}
              </Button>
              <Button type="submit" size="lg" className="flex-1" disabled={submitting}>
                {submitting ? text.submitting : text.next}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
