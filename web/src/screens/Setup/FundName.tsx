import type { FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'

const text = copy.setup

/**
 * Setup step 1 of 4: name the fund. No request fires here - the name only
 * leaves the browser once locations are chosen too, in one
 * POST /api/setup (Setup.tsx).
 */
export default function FundName({
  name,
  onChange,
  onNext,
}: {
  name: string
  onChange: (name: string) => void
  onNext: () => void
}) {
  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (name.trim() === '') return
    onNext()
  }

  return (
    <main className="flex min-h-dvh items-center justify-center p-6">
      <Card className="w-full max-w-sm shadow-card" size="default">
        <CardHeader>
          <p className="text-sm text-muted-foreground">{text.stepLabel(1)}</p>
          <CardTitle className="text-2xl font-semibold">{text.fund.heading}</CardTitle>
          <p className="text-muted-foreground">{text.fund.body}</p>
        </CardHeader>
        <CardContent>
          <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="setup-fund-name">{text.fund.nameLabel}</Label>
              <Input
                id="setup-fund-name"
                type="text"
                required
                value={name}
                onChange={(event) => onChange(event.target.value)}
              />
            </div>
            <Button type="submit" size="lg" disabled={name.trim() === ''}>
              {text.next}
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
