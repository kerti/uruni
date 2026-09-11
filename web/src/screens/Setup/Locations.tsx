import type { FormEvent } from 'react'
import { Plus, X } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { copy } from '@/copy/id'
import { newLocationRow } from '@/screens/Setup/locationRow'
import type { LocationRow } from '@/screens/Setup/locationRow'

const text = copy.setup

/**
 * Setup step 2 of 4: choose and name every location the fund starts with.
 * Seeded with one cash and one bank row (#78's resolution) - both editable
 * and removable down to a floor of one, and a row can be added. Submitting
 * fires no request of its own (#230) - it only advances to step 3, where the
 * opening balance for each of these rows is collected before the wizard's
 * one POST /api/setup fires with everything at once. Rows are keyed by their
 * own `clientId`, never by array index, so removing an earlier row here
 * cannot shift which row's opening balance a later step is editing.
 */
export default function Locations({
  rows,
  onChange,
  onNext,
  onBack,
}: {
  rows: LocationRow[]
  onChange: (rows: LocationRow[]) => void
  onNext: () => void
  onBack: () => void
}) {
  function updateRow(clientId: string, patch: Partial<LocationRow>) {
    onChange(rows.map((row) => (row.clientId === clientId ? { ...row, ...patch } : row)))
  }

  function addRow() {
    onChange([...rows, newLocationRow()])
  }

  function removeRow(clientId: string) {
    if (rows.length <= 1) return
    onChange(rows.filter((row) => row.clientId !== clientId))
  }

  const canSubmit = rows.every((row) => row.name.trim() !== '')

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!canSubmit) return
    onNext()
  }

  return (
    <main className="flex min-h-dvh items-center justify-center p-6">
      <Card className="w-full max-w-sm shadow-card" size="default">
        <CardHeader>
          <p className="text-sm text-muted-foreground">{text.stepLabel(2)}</p>
          <CardTitle className="text-2xl font-semibold">{text.locations.heading}</CardTitle>
          <p className="text-muted-foreground">{text.locations.body}</p>
        </CardHeader>
        <CardContent>
          <form className="flex flex-col gap-4" onSubmit={handleSubmit} noValidate>
            {rows.map((row) => (
              <div key={row.clientId} className="flex flex-col gap-1.5 rounded-lg border border-border p-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor={`setup-location-kind-${row.clientId}`}>{text.locations.kindLabel}</Label>
                  <Select value={row.kind} onValueChange={(next) => updateRow(row.clientId, { kind: next as 'cash' | 'bank' })}>
                    <SelectTrigger id={`setup-location-kind-${row.clientId}`} aria-label={text.locations.kindLabel}>
                      <SelectValue>{row.kind === 'bank' ? text.locations.kindBank : text.locations.kindCash}</SelectValue>
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="cash">{text.locations.kindCash}</SelectItem>
                      <SelectItem value="bank">{text.locations.kindBank}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor={`setup-location-name-${row.clientId}`}>{text.locations.nameLabel}</Label>
                  <Input
                    id={`setup-location-name-${row.clientId}`}
                    type="text"
                    required
                    value={row.name}
                    onChange={(event) => updateRow(row.clientId, { name: event.target.value })}
                  />
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="self-end text-muted-foreground disabled:opacity-30"
                  onClick={() => removeRow(row.clientId)}
                  disabled={rows.length <= 1}
                  aria-label={text.locations.removeRow}
                >
                  <X aria-hidden="true" />
                  {text.locations.removeRow}
                </Button>
              </div>
            ))}
            {rows.length <= 1 && <p className="text-sm text-muted-foreground">{text.locations.minOneLocation}</p>}
            <Button type="button" variant="outline" size="lg" onClick={addRow}>
              <Plus aria-hidden="true" />
              {text.locations.addRow}
            </Button>
            <div className="flex gap-2">
              <Button type="button" variant="outline" size="lg" onClick={onBack}>
                {text.back}
              </Button>
              <Button type="submit" size="lg" className="flex-1" disabled={!canSubmit}>
                {text.next}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
