import { useId } from 'react'

import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { Account } from '@/lib/accounts'

/**
 * Picks the location ("lokasi") a transaction is recorded against. A retired
 * account (`inactive_on` set) is excluded entirely - PRD section 7.2's
 * location field is for recording new money, not browsing history.
 * RecordTransaction.tsx applies the same exclusion when choosing the
 * *default* selection, which must also fall back past a
 * remembered-but-now-retired choice.
 *
 * Renders through the themed Select (`radix-ui` is already a dependency, so
 * this costs styling, not a new package).
 */
export default function AccountPicker({
  id,
  label,
  accounts,
  value,
  onChange,
  disabled,
}: {
  id?: string
  label: string
  accounts: Account[]
  value: number | null
  onChange: (accountId: number) => void
  disabled?: boolean
}) {
  // Same fallback AmountInput uses: `id` is optional, and a Label whose
  // htmlFor is undefined stops labelling anything.
  const autoId = useId()
  const selectId = id ?? autoId
  const activeAccounts = accounts.filter((account) => account.inactive_on === null)

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={selectId}>{label}</Label>
      <Select
        value={value === null ? '' : String(value)}
        onValueChange={(next) => {
          // Radix emits '' for "nothing selected" on mount; Number('') is 0,
          // which looks like an id and is not one.
          if (next !== '') onChange(Number(next))
        }}
        disabled={disabled || activeAccounts.length === 0}
      >
        <SelectTrigger id={selectId} aria-label={label}>
          <SelectValue>{activeAccounts.find((account) => account.id === value)?.name}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          {activeAccounts.map((account) => (
            <SelectItem key={account.id} value={String(account.id)}>
              {account.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}
