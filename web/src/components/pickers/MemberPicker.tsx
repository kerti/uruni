import { useId } from 'react'

import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { copy } from '@/copy/id'
import type { Member } from '@/lib/setup'

/**
 * Picks a member from the fund's roster. An inactive member (`inactive_on`
 * set) is excluded entirely, as AccountPicker does for retired locations: PRD
 * section 7.4's reimbursement form is for active members, not for browsing
 * history. Follows AccountPicker's shape, through the themed Select.
 */
export default function MemberPicker({
  id,
  label,
  members,
  value,
  onChange,
  placeholder,
  disabled,
}: {
  id?: string
  label: string
  members: Member[]
  value: number | null
  onChange: (memberId: number) => void
  placeholder?: string
  disabled?: boolean
}) {
  const autoId = useId()
  const selectId = id ?? autoId
  const activeMembers = members.filter((member) => member.inactive_on === null)

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={selectId}>{label}</Label>
      <Select
        value={value === null ? '' : String(value)}
        onValueChange={(next) => {
          if (next !== '') onChange(Number(next))
        }}
        disabled={disabled || activeMembers.length === 0}
      >
        <SelectTrigger id={selectId} aria-label={label}>
          <SelectValue>{activeMembers.find((member) => member.id === value)?.name ?? placeholder}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          {activeMembers.map((member) => (
            <SelectItem key={member.id} value={String(member.id)}>
              {member.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

/** The sentinel Select item value for "no member chosen" - Radix's Select
 * refuses an empty-string item value, so a clearable option needs one that
 * can never collide with a real member id. */
const NONE_VALUE = '__none__'

/**
 * A member field that may be left blank (ADR-034): Catat's "Dari siapa?
 * (opsional)" - a contribution may name a guest or stay anonymous. Its own
 * export rather than widening MemberPicker's onChange (required in
 * Reimbursements.tsx) to a nullable signature, which every existing caller
 * would have to handle.
 *
 * An explicit "Tidak disebutkan" item is the way back to null once a member
 * is chosen - Radix's Select has no built-in clear.
 */
export function OptionalMemberPicker({
  id,
  label,
  members,
  value,
  onChange,
  disabled,
}: {
  id?: string
  label: string
  members: Member[]
  value: number | null
  onChange: (memberId: number | null) => void
  disabled?: boolean
}) {
  const autoId = useId()
  const selectId = id ?? autoId
  const activeMembers = members.filter((member) => member.inactive_on === null)
  const chosenName = activeMembers.find((member) => member.id === value)?.name ?? copy.record.contributorNone

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={selectId}>{label}</Label>
      <Select
        value={value === null ? NONE_VALUE : String(value)}
        onValueChange={(next) => onChange(next === NONE_VALUE ? null : Number(next))}
        disabled={disabled}
      >
        <SelectTrigger id={selectId} aria-label={label}>
          <SelectValue>{chosenName}</SelectValue>
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={NONE_VALUE}>{copy.record.contributorNone}</SelectItem>
          {activeMembers.map((member) => (
            <SelectItem key={member.id} value={String(member.id)}>
              {member.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

/**
 * Zero-or-more members an envelope is *for* (ADR-034's recipients): a plain
 * checklist, since the themed Select has no multi-select shape. Each row is a
 * native checkbox with its label wrapping it, so the whole row is the 44px
 * touch target.
 *
 * Active members only, as every member-choosing control here - though one
 * already saved as a recipient stays listed. The set is small and
 * rarely-changed, so no retired-row handling as in AccountPicker.
 */
export function MemberMultiPicker({
  label,
  members,
  value,
  onChange,
  emptyMessage,
  disabled,
}: {
  label: string
  members: Member[]
  value: number[]
  onChange: (memberIds: number[]) => void
  /** Shown in place of the list when the fund has no member this control could
   * offer (never members.length === 0 alone - a saved recipient who has since
   * gone inactive still counts as something to show). */
  emptyMessage: string
  disabled?: boolean
}) {
  const groupId = useId()
  const selectable = members.filter((member) => member.inactive_on === null || value.includes(member.id))

  function toggle(memberId: number) {
    if (value.includes(memberId)) {
      onChange(value.filter((id) => id !== memberId))
    } else {
      onChange([...value, memberId])
    }
  }

  return (
    <fieldset className="flex flex-col gap-1.5">
      <legend id={groupId} className="px-0 text-sm font-medium">
        {label}
      </legend>
      {selectable.length === 0 ? (
        <p className="text-sm text-muted-foreground">{emptyMessage}</p>
      ) : (
        <ul aria-labelledby={groupId} className="flex flex-col gap-1 rounded-lg ring-1 ring-foreground/10">
          {selectable.map((member) => (
            <li key={member.id}>
              <label className="flex min-h-11 w-full items-center gap-2 px-3 py-2 select-none">
                <input
                  type="checkbox"
                  className="size-4 shrink-0"
                  checked={value.includes(member.id)}
                  onChange={() => toggle(member.id)}
                  disabled={disabled}
                />
                <span className="truncate">{member.name}</span>
              </label>
            </li>
          ))}
        </ul>
      )}
    </fieldset>
  )
}
