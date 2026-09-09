import { useId } from 'react'

import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { Member } from '@/lib/setup'

/**
 * Picks a member from the fund's roster. An inactive member (`inactive_on`
 * set) is excluded from the selectable list entirely — the same pattern
 * AccountPicker uses for retired locations: PRD §7.4's reimbursement
 * record form is for active members, not for browsing history.
 *
 * Renders through the themed Select (M6.15), following AccountPicker's
 * exact shape.
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
