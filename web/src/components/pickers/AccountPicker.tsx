import { useId } from 'react'

import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { Account } from '@/lib/accounts'

/**
 * Picks the location ("lokasi") a transaction is recorded against. A
 * retired account (`inactive_on` set) is excluded from the selectable list
 * entirely - PRD §7.2's location field is for recording new money, not for
 * browsing history, and #141's own definition of done: a location retired
 * through account lifecycle (M6's account-lifecycle slice) must not be
 * pickable for a fresh entry. RecordTransaction.tsx is responsible for the
 * same exclusion when choosing the *default* selection, since that default
 * also has to fall back past a remembered-but-now-retired choice.
 *
 * Renders through the themed Select (M6.15). It was a native <select>
 * until then, on the grounds that the design system had no select of its
 * own and one should not be invented for a picker - what changed is that
 * `radix-ui` was already a dependency, so the themed control costs styling
 * rather than a new package.
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
  // htmlFor is undefined silently stops labelling anything.
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
