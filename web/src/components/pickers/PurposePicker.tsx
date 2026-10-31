import { useId, type ReactNode } from 'react'

import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { Purpose } from '@/lib/purposes'

/**
 * Picks the purpose tag ("pos") a transaction carries - every row
 * GET /api/purposes returns (main, any pass-through, any open incidental).
 * Unlike AccountPicker there is no retirement to exclude. RecordTransaction.tsx
 * picks the default (the `kind: "main"` row, PRD section 7.2), which must also
 * survive a fund with no purposes loaded yet.
 */
export default function PurposePicker({
  id,
  label,
  aside,
  purposes,
  value,
  onChange,
  disabled,
}: {
  id?: string
  label: string
  /** A short figure on the label's row, right-aligned: the balance of what
   * is picked (#749), where a line under the field cost a row each. */
  aside?: ReactNode
  purposes: Purpose[]
  value: number | null
  onChange: (purposeId: number) => void
  disabled?: boolean
}) {
  // Same fallback AmountInput uses: `id` is optional, and a Label whose
  // htmlFor is undefined silently stops labelling anything.
  const autoId = useId()
  const selectId = id ?? autoId

  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-3">
        <Label htmlFor={selectId}>{label}</Label>
        {aside && <span className="tabular text-sm text-muted-foreground">{aside}</span>}
      </div>
      {/* Radix's Select, not the native one it replaced (M6.15): the
          browser's own list was the one control in the app that never
          looked like the app. Values cross the boundary as strings, so the
          id is stringified going in and parsed coming back out - and the
          empty string is filtered out on the way back, because Radix emits
          one for "nothing selected" on mount and Number('') is 0, a
          perfectly valid-looking id that is not any row. */}
      <Select
        value={value === null ? '' : String(value)}
        onValueChange={(next) => {
          if (next !== '') onChange(Number(next))
        }}
        disabled={disabled || purposes.length === 0}
      >
        <SelectTrigger id={selectId} aria-label={label}>
          <SelectValue>{purposes.find((purpose) => purpose.id === value)?.name}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          {purposes.map((purpose) => (
            <SelectItem key={purpose.id} value={String(purpose.id)}>
              {purpose.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}
