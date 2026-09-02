import { useId } from 'react'

import { Label } from '@/components/ui/label'
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
 * A native <select>, not a new shadcn component - Design-System has no
 * picker/select component of its own yet, and CLAUDE.md's scope discipline
 * says don't pull one in for this.
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
      <select
        id={selectId}
        className="h-11 w-full rounded-lg border border-input bg-transparent px-3 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 md:text-sm"
        value={value ?? ''}
        onChange={(event) => onChange(Number(event.target.value))}
        disabled={disabled || activeAccounts.length === 0}
      >
        {activeAccounts.map((account) => (
          <option key={account.id} value={account.id}>
            {account.name}
          </option>
        ))}
      </select>
    </div>
  )
}
