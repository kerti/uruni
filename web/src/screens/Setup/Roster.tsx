import type { FormEvent } from 'react'
import { Plus, X } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { formatRupiahDigits, parseRupiah } from '@/lib/money'
import type { ApiError } from '@/lib/api'

const text = copy.setup

/**
 * Setup step 4 of 4, fully optional and fully skippable (the issue's own
 * settled ruling: nothing in the schema requires a fund to have any members
 * or tiers, and M6.16/M6.17 give a full roster screen for later). Skip fires
 * no request at all; Finish creates only what she actually filled in -
 * Setup.tsx decides which calls that is.
 */
export default function Roster({
  tierName,
  onTierNameChange,
  rateAmount,
  onRateAmountChange,
  members,
  onMembersChange,
  onSkip,
  onFinish,
  submitting,
  error,
}: {
  tierName: string
  onTierNameChange: (value: string) => void
  rateAmount: string
  onRateAmountChange: (value: string) => void
  members: string[]
  onMembersChange: (members: string[]) => void
  onSkip: () => void
  onFinish: () => void
  submitting: boolean
  error: ApiError | undefined
}) {
  function handleRateChange(raw: string) {
    const digits = parseRupiah(raw)
    onRateAmountChange(digits === 0 ? '' : formatRupiahDigits(digits))
  }

  function updateMember(index: number, value: string) {
    onMembersChange(members.map((name, i) => (i === index ? value : name)))
  }

  function addMember() {
    onMembersChange([...members, ''])
  }

  function removeMember(index: number) {
    onMembersChange(members.filter((_, i) => i !== index))
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    onFinish()
  }

  return (
    <main className="flex min-h-dvh items-center justify-center p-6">
      <Card className="w-full max-w-sm shadow-card" size="default">
        <CardHeader>
          <p className="text-sm text-muted-foreground">{text.stepLabel(4)}</p>
          <CardTitle className="text-2xl font-semibold">{text.roster.heading}</CardTitle>
          <p className="text-muted-foreground">{text.roster.body}</p>
        </CardHeader>
        <CardContent>
          <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="setup-roster-tier-name">{text.roster.tierNameLabel}</Label>
              <Input
                id="setup-roster-tier-name"
                type="text"
                value={tierName}
                onChange={(event) => onTierNameChange(event.target.value)}
                disabled={submitting}
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="setup-roster-rate-amount">{text.roster.rateAmountLabel}</Label>
              <Input
                id="setup-roster-rate-amount"
                type="text"
                inputMode="numeric"
                value={rateAmount}
                onChange={(event) => handleRateChange(event.target.value)}
                // A rate belongs to a tier (POST /api/dues-tiers/{id}/rates
                // takes one in its path), so an amount typed with no tier
                // named has nowhere to go - Setup.tsx would drop it
                // silently. Held closed until there is a tier to hang it on.
                disabled={submitting || tierName.trim() === ''}
              />
            </div>
            {members.map((name, index) => (
              <div key={index} className="flex items-end gap-2">
                <div className="flex flex-1 flex-col gap-1.5">
                  <Label htmlFor={`setup-roster-member-${index}`}>{text.roster.memberNameLabel}</Label>
                  <Input
                    id={`setup-roster-member-${index}`}
                    type="text"
                    value={name}
                    onChange={(event) => updateMember(index, event.target.value)}
                    disabled={submitting}
                  />
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  aria-label={text.roster.removeMember}
                  onClick={() => removeMember(index)}
                  disabled={submitting}
                >
                  <X aria-hidden="true" />
                </Button>
              </div>
            ))}
            <Button type="button" variant="outline" onClick={addMember} disabled={submitting}>
              <Plus aria-hidden="true" />
              {text.roster.addMember}
            </Button>
            {error && <ErrorState error={error} />}
            <div className="flex gap-2">
              <Button type="button" variant="outline" onClick={onSkip} disabled={submitting}>
                {text.roster.skip}
              </Button>
              <Button type="submit" size="lg" className="flex-1" disabled={submitting}>
                {submitting ? text.submitting : text.roster.finish}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
