import { useId } from 'react'

import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { Purpose } from '@/lib/purposes'

/**
 * Picks the purpose tag ("peruntukan") a transaction carries - every row
 * GET /api/purposes returns (main, any pass-through, any open incidental),
 * unlike AccountPicker there is no retirement to exclude: a purpose is never
 * deactivated. RecordTransaction.tsx picks the default (the `kind: "main"`
 * row, PRD §7.2) since that default also has to survive a fund with no
 * purposes loaded yet.
 */
export default function PurposePicker({
  id,
  label,
  purposes,
  value,
  onChange,
  disabled,
}: {
  id?: string
  label: string
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
      <Label htmlFor={selectId}>{label}</Label>
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
