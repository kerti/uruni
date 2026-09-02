import { useId } from 'react'

import { Label } from '@/components/ui/label'
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
      <select
        id={selectId}
        className="h-11 w-full rounded-lg border border-input bg-transparent px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 md:text-sm"
        value={value ?? ''}
        onChange={(event) => onChange(Number(event.target.value))}
        disabled={disabled || purposes.length === 0}
      >
        {purposes.map((purpose) => (
          <option key={purpose.id} value={purpose.id}>
            {purpose.name}
          </option>
        ))}
      </select>
    </div>
  )
}
