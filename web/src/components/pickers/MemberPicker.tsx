import { Plus, X } from '@phosphor-icons/react'
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
 * Zero-or-more members an envelope is *for* (ADR-034's recipients): only the
 * chosen ones are listed, each with an X that takes it off the list, and a
 * member picker below adds one more (#768). A checklist of the whole roster
 * put an unbounded list inside a dialog (ADR-032) and pushed the dialog's
 * button far below a real roster; an envelope is for one or two people, and
 * the roster stays in the picker, where every member field keeps it.
 *
 * Active members only, as every member-choosing control here - though one
 * already saved as a recipient stays listed until she removes it.
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
  /** Shown in place of the picker when the fund has no member this control
   * could offer and none is chosen. */
  emptyMessage: string
  disabled?: boolean
}) {
  const groupId = useId()
  const pickerId = useId()
  const text = copy.incidentals.open
  const chosen = value.map((id) => members.find((member) => member.id === id)).filter((member): member is Member => member !== undefined)
  const addable = members.filter((member) => member.inactive_on === null && !value.includes(member.id))

  return (
    // min-w-0: a fieldset defaults to min-inline-size: min-content, so one
    // long name would push the list and its X past the dialog's edge.
    <fieldset className="flex min-w-0 flex-col gap-1.5">
      <legend id={groupId} className="px-0 text-sm font-medium">
        {label}
      </legend>
      {chosen.length > 0 && (
        <ul aria-labelledby={groupId} className="flex flex-col rounded-lg ring-1 ring-foreground/10">
          {chosen.map((member) => (
            <li
              key={member.id}
              className="flex min-h-11 items-center justify-between gap-2 border-t border-foreground/10 pl-3 first:border-t-0"
            >
              <span className="min-w-0 truncate">{member.name}</span>
              <button
                type="button"
                aria-label={text.recipientRemoveAria(member.name)}
                disabled={disabled}
                onClick={() => onChange(value.filter((id) => id !== member.id))}
                className="flex size-11 shrink-0 items-center justify-center text-muted-foreground transition-colors outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50"
              >
                <X aria-hidden="true" className="size-4" />
              </button>
            </li>
          ))}
        </ul>
      )}
      {addable.length > 0 ? (
        // Always empty: picking a member adds it to the list above and the
        // picker is ready for the next one.
        <Select
          value=""
          onValueChange={(next) => {
            if (next !== '') onChange([...value, Number(next)])
          }}
          disabled={disabled}
        >
          <SelectTrigger id={pickerId} aria-label={`${label} ${text.recipientsAdd}`}>
            <span className="flex min-w-0 items-center gap-2 text-muted-foreground">
              <Plus aria-hidden="true" className="size-4 shrink-0" />
              <span className="truncate">{text.recipientsAdd}</span>
            </span>
          </SelectTrigger>
          <SelectContent>
            {addable.map((member) => (
              <SelectItem key={member.id} value={String(member.id)}>
                {member.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      ) : (
        chosen.length === 0 && <p className="text-sm text-muted-foreground">{emptyMessage}</p>
      )}
    </fieldset>
  )
}
