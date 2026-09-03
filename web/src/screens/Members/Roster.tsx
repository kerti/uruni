import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { createMember, deleteMember, listDuesTiers, listMembers, updateMember } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import type { DuesTier, Member } from '@/lib/setup'

const text = copy.members.roster

/** The value the tier select carries for "no tier at all" - a real state
 * (a member who owes no dues), not an empty field. Radix item values must be
 * non-empty strings, so it cannot simply be ''. */
const NO_TIER = 'none'

/** Local YYYY-MM-DD - never toISOString(), which is UTC and can read a day
 * early in WIB. Same helper as RecordTransaction.tsx's todayISODate. */
function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

interface RosterData {
  members: Member[]
  tiers: DuesTier[]
}

function tierName(tiers: DuesTier[], tierId: number | null): string {
  if (tierId === null) return text.tierNone
  return tiers.find((tier) => tier.id === tierId)?.name ?? text.tierNone
}

/**
 * The roster (M6.16, PRD §6): the full version of what the setup wizard
 * sketched as an optional, skippable step.
 *
 * Every member is listed, retired ones included - dues status and history
 * still name them, and this is where one gets reinstated. Deactivating is
 * the right action for someone who actually left: `inactive_on` bounds the
 * window `DuesStatusForPeriod` walks, so no further month is owed, while
 * everything already recorded stays exactly as it was. Delete is only for a
 * duplicate typed twice, and the foreign key refuses it the moment anything
 * references the row.
 *
 * A member with no tier is an ordinary state, not an unfinished one: no
 * tier means no dues obligation, which is why the select carries an explicit
 * "Tanpa golongan" rather than an empty option.
 */
export default function Roster({ tiersVersion }: { tiersVersion: number }) {
  const [state, run] = useApi<RosterData>()

  async function load(): Promise<RosterData> {
    const [members, tiers] = await Promise.all([listMembers(), listDuesTiers()])
    return { members, tiers }
  }

  useEffect(() => {
    void run(load)
    // run is a stable useCallback (useApi.ts); tiersVersion is the
    // deliberate extra dependency - the section below this one writes the
    // tiers this screen's picker reads, and without it a rename shows here
    // only after leaving the screen and coming back.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run, tiersVersion])

  function reload() {
    void run(load)
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      {state.status === 'idle' || state.status === 'loading' ? (
        <Loading />
      ) : state.status === 'error' || !state.data ? (
        state.error && <ErrorState error={state.error} onRetry={reload} />
      ) : (
        <>
          {state.data.members.length === 0 ? (
            <p className="text-muted-foreground">{text.empty}</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {state.data.members.map((member) => (
                <MemberRow key={member.id} member={member} tiers={state.data!.tiers} onChanged={reload} />
              ))}
            </ul>
          )}
          <AddMember tiers={state.data.tiers} onAdded={reload} />
        </>
      )}
    </section>
  )
}

/** One member, with its four actions. Each row owns its request state so a
 * failure on one never blanks the others - the shape LocationRow uses. */
function MemberRow({ member, tiers, onChanged }: { member: Member; tiers: DuesTier[]; onChanged: () => void }) {
  const [state, run] = useApi<unknown>()
  const [editing, setEditing] = useState(false)
  const [name, setName] = useState(member.name)
  const [tierId, setTierId] = useState<number | null>(member.tier_id)
  const [joinedOn, setJoinedOn] = useState(member.joined_on ?? '')

  const busy = state.status === 'loading'
  const inactive = member.inactive_on !== null

  async function submit(fn: () => Promise<unknown>) {
    await run(fn)
    onChanged()
  }

  function handleEdit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed === '') {
      setEditing(false)
      return
    }
    // Only what actually changed goes on the wire: an absent key means
    // "leave alone" server-side, and an explicit null means "clear it" -
    // which is how a member's tier is dropped and how a joined-on date is
    // erased back to "always was a member".
    void submit(async () => {
      const updated = await updateMember(member.id, {
        name: trimmed === member.name ? undefined : trimmed,
        tierId: tierId === member.tier_id ? undefined : tierId,
        joinedOn: joinedOn === (member.joined_on ?? '') ? undefined : joinedOn === '' ? null : joinedOn,
      })
      setEditing(false)
      return updated
    })
  }

  return (
    <li className="flex flex-col gap-2 rounded-lg bg-card px-4 py-3 ring-1 ring-foreground/10">
      {editing ? (
        <form className="flex flex-col gap-2" onSubmit={handleEdit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={`member-name-${member.id}`}>{text.nameLabel}</Label>
            <Input
              id={`member-name-${member.id}`}
              type="text"
              value={name}
              autoFocus
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={`member-tier-${member.id}`}>{text.tierLabel}</Label>
            <Select
              value={tierId === null ? NO_TIER : String(tierId)}
              onValueChange={(next) => setTierId(next === NO_TIER ? null : Number(next))}
            >
              <SelectTrigger id={`member-tier-${member.id}`} aria-label={text.tierLabel}>
                <SelectValue>{tierName(tiers, tierId)}</SelectValue>
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NO_TIER}>{text.tierNone}</SelectItem>
                {tiers.map((tier) => (
                  <SelectItem key={tier.id} value={String(tier.id)}>
                    {tier.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor={`member-joined-${member.id}`}>{text.joinedOnLabel}</Label>
            <Input
              id={`member-joined-${member.id}`}
              type="date"
              value={joinedOn}
              onChange={(event) => setJoinedOn(event.target.value)}
            />
          </div>
          <div className="flex gap-2">
            <Button type="submit" className="h-11" disabled={busy}>
              {busy ? text.saving : text.save}
            </Button>
            <Button
              type="button"
              variant="ghost"
              className="h-11"
              disabled={busy}
              onClick={() => {
                setName(member.name)
                setTierId(member.tier_id)
                setJoinedOn(member.joined_on ?? '')
                setEditing(false)
              }}
            >
              {text.cancel}
            </Button>
          </div>
        </form>
      ) : (
        <>
          <div className="flex items-baseline justify-between gap-3">
            <span className="min-w-0 truncate font-medium">{member.name}</span>
            <span className="shrink-0 text-sm text-muted-foreground">{tierName(tiers, member.tier_id)}</span>
          </div>
          {inactive && <span className="text-sm text-muted-foreground">{text.inactiveBadge}</span>}
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={() => setEditing(true)}>
              {text.edit}
            </Button>
            {inactive ? (
              <Button
                type="button"
                variant="outline"
                className="h-11"
                disabled={busy}
                onClick={() => void submit(() => updateMember(member.id, { inactiveOn: null }))}
              >
                {busy ? text.reinstating : text.reinstate}
              </Button>
            ) : (
              <Button
                type="button"
                variant="outline"
                className="h-11"
                disabled={busy}
                onClick={() => void submit(() => updateMember(member.id, { inactiveOn: todayISODate() }))}
              >
                {busy ? text.deactivating : text.deactivate}
              </Button>
            )}
            <Button
              type="button"
              variant="ghost"
              className="h-11 text-destructive"
              disabled={busy}
              onClick={() => void submit(() => deleteMember(member.id))}
            >
              {busy ? text.deleting : text.delete}
            </Button>
          </div>
        </>
      )}

      {/* The 409 gets its own sentence pointing at deactivate, exactly as the
          locations section does - a refusal that says what to do next is the
          difference between a wall and a signpost. */}
      {state.status === 'error' &&
        state.error &&
        (state.error instanceof ApiError && state.error.code === 'referenced_by_other_records' ? (
          <p role="alert" className="text-sm text-attention">
            {text.deleteRefused}
          </p>
        ) : (
          <ErrorState error={state.error} />
        ))}
    </li>
  )
}

/** Adding a member after setup - the wizard's optional step, available for
 * the rest of the fund's life. joined_on defaults to today, per #187: a
 * fund's history starts at adoption, so a new member owes from now, and
 * backdating is the deliberate exception for arrears actually owed. */
function AddMember({ tiers, onAdded }: { tiers: DuesTier[]; onAdded: () => void }) {
  const [state, run] = useApi<Member>()
  const [name, setName] = useState('')
  const [tierId, setTierId] = useState<number | null>(null)
  const [joinedOn, setJoinedOn] = useState(todayISODate)

  const busy = state.status === 'loading'

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed === '') return
    void run(async () => {
      const created = await createMember(trimmed, tierId, joinedOn === '' ? null : joinedOn)
      setName('')
      setTierId(null)
      setJoinedOn(todayISODate())
      onAdded()
      return created
    })
  }

  return (
    /* Named as a region: its labels are the same ones every row being
       edited shows, so a bare getByLabelText would be ambiguous - to a
       screen reader as much as to a test. */
    <form aria-label={text.add} className="flex flex-col gap-3 rounded-lg border border-border p-3" onSubmit={handleSubmit} noValidate>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="new-member-name">{text.nameLabel}</Label>
        <Input id="new-member-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="new-member-tier">{text.tierLabel}</Label>
        <Select
          value={tierId === null ? NO_TIER : String(tierId)}
          onValueChange={(next) => setTierId(next === NO_TIER ? null : Number(next))}
        >
          <SelectTrigger id="new-member-tier" aria-label={text.tierLabel}>
            <SelectValue>{tierName(tiers, tierId)}</SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={NO_TIER}>{text.tierNone}</SelectItem>
            {tiers.map((tier) => (
              <SelectItem key={tier.id} value={String(tier.id)}>
                {tier.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor="new-member-joined">{text.joinedOnLabel}</Label>
        <Input id="new-member-joined" type="date" value={joinedOn} onChange={(event) => setJoinedOn(event.target.value)} />
      </div>
      <Button type="submit" className="h-11 self-start" disabled={busy || name.trim() === ''}>
        {busy ? text.adding : text.add}
      </Button>
      {state.status === 'error' && state.error && <ErrorState error={state.error} />}
    </form>
  )
}
