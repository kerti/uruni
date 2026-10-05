import { useEffect, useId, useState, type FormEvent } from 'react'

import MonthField from '@/components/MonthField'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { copy } from '@/copy/id'
import { periodBounds } from '@/lib/dates'
import type { Purpose } from '@/lib/purposes'
import { noFilters, type TransactionFilters } from '@/lib/transactionFilters'
import type { Member } from '@/lib/setup'

const text = copy.history.transactions

/** Radix's Select refuses an empty-string item value, so "Semua" needs a
 * sentinel no id can collide with (OptionalMemberPicker's own trick). */
const ALL = '__all__'

function FilterSelect({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: string | null
  options: { value: string; label: string }[]
  onChange: (value: string | null) => void
}) {
  const id = useId()
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      <Select value={value ?? ALL} onValueChange={(next) => onChange(next === ALL ? null : next)}>
        <SelectTrigger id={id} aria-label={label}>
          <SelectValue>{options.find((option) => option.value === value)?.label ?? text.filterAll}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={ALL}>{text.filterAll}</SelectItem>
          {options.map((option) => (
            <SelectItem key={option.value} value={option.value}>
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

/**
 * Where she picks Transaksi's filters (#424). Edits a draft and applies it
 * only on "Tampilkan", so a half-chosen set never fires a request per
 * field. Every purpose is offered, a closed envelope's too - this list is
 * that envelope's record (ADR-032) - and every member, an inactive one too,
 * since their rows are still history.
 */
export default function TransactionFilterDialog({
  open,
  filters,
  purposes,
  members,
  onApply,
  onClose,
}: {
  open: boolean
  filters: TransactionFilters
  purposes: Purpose[]
  members: Member[]
  onApply: (filters: TransactionFilters) => void
  onClose: () => void
}) {
  const [draft, setDraft] = useState<TransactionFilters>(filters)
  const monthId = useId()

  // A fresh draft every time the dialog opens, seeded with what is applied.
  useEffect(() => {
    if (open) setDraft(filters)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    onApply(draft)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{text.filterHeading}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center justify-between">
              <Label htmlFor={monthId}>{text.filterMonth}</Label>
              {/* The month grid has no "all" cell, so the way back to every
                  month sits beside its label - only once there is a month
                  to clear. */}
              {draft.month !== null && (
                <button type="button" className="text-sm font-medium text-primary" onClick={() => setDraft((d) => ({ ...d, month: null }))}>
                  {text.filterAll}
                </button>
              )}
            </div>
            <MonthField
              id={monthId}
              value={draft.month ?? ''}
              onChange={(month) => setDraft((d) => ({ ...d, month }))}
              bounds={periodBounds.history()}
              emptyLabel={text.filterAll}
            />
          </div>
          <FilterSelect
            label={text.filterPurpose}
            value={draft.purposeId === null ? null : String(draft.purposeId)}
            options={purposes.map((purpose) => ({ value: String(purpose.id), label: purpose.name }))}
            onChange={(value) => setDraft((d) => ({ ...d, purposeId: value === null ? null : Number(value) }))}
          />
          <FilterSelect
            label={text.filterMember}
            value={draft.memberId === null ? null : String(draft.memberId)}
            options={members.map((member) => ({ value: String(member.id), label: member.name }))}
            onChange={(value) => setDraft((d) => ({ ...d, memberId: value === null ? null : Number(value) }))}
          />
          <FilterSelect
            label={copy.record.directionLabel}
            value={draft.direction}
            options={[
              { value: 'in', label: copy.record.directionIn },
              { value: 'out', label: copy.record.directionOut },
            ]}
            onChange={(value) => setDraft((d) => ({ ...d, direction: value === 'in' || value === 'out' ? value : null }))}
          />
          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" size="lg" onClick={() => onApply(noFilters)}>
              {text.filterReset}
            </Button>
            <Button type="submit" size="lg">
              {text.filterApply}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
