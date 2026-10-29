import { Search } from 'lucide-react'
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'

import DateField from '@/components/DateField'
import MonthField from '@/components/MonthField'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError, errorCode } from '@/lib/api'
import { todayISODate, dateBounds, periodBounds } from '@/lib/dates'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { formatIDR } from '@/lib/money'
import { createMember, deleteMember, listDuesTiers, listMembersPage, updateMember } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
import { useResetWhen } from '@/lib/useResetWhen'
import type { DuesTier, Member, MembersPage } from '@/lib/setup'

const text = copy.members.roster

/** Long enough that a word typed at normal speed is one request, short enough
 * that the list follows her without a visible pause - the budget
 * History/Transactions.tsx spends on its search field. */
const SEARCH_DEBOUNCE_MS = 300

/** The "no tier" option: a real state (a member who owes no dues), not an
 * empty field. Radix item values must be non-empty strings, so it cannot be
 * ''. */
const NO_TIER = 'none'

interface FirstPage {
  page: MembersPage
  /** For the add/edit dialog's tier picker. Loaded with the first page so
   * opening the dialog costs no further request. */
  tiers: DuesTier[]
}

/**
 * Anggota's roster (ADR-032 "Lists: paging and search" and "The roster row,
 * and a word that does not exist yet"): card list, dialog editing,
 * keyset-paged and searchable by name on the server. Borrows its shape from
 * Settings/Locations.tsx (card-plus-dialog) and History/Transactions.tsx
 * (paging and search).
 *
 * Every member is listed, retired ones included - dues status and history
 * still name them, and this is where one gets reinstated. Deactivating is the
 * right action for someone who left: `inactive_on` bounds the window
 * `DuesStatusForPeriod` walks, so no further month is owed, while everything
 * recorded stays as it was. Delete is only for a duplicate typed twice, and
 * the foreign key refuses it once anything references the row.
 *
 * Each row shows name, tier name, current rate and the Tunggakan badge - the
 * first three period-independent and straight off the row GET /api/members
 * returns, the badge computed server-side through the same ledger path
 * GET /api/members/{id}/outstanding-dues uses (CLAUDE.md rule 2: one source
 * of truth for what is owed). It is absent when arrears_months is 0 and never
 * reuses copy.dues.statuses' words - that vocabulary reads one period at a
 * time and this one is cumulative, and one word for both would put two
 * disagreeing readings of it on two screens (ADR-032).
 */
export default function Roster() {
  const [searchParams, setSearchParams] = useSearchParams()
  const q = (searchParams.get('q') ?? '').trim()
  const [draft, setDraft] = useState(q)

  const { value: dialogValue, open, close, clear } = useDialogParam()

  const [state, run] = useApi<FirstPage>()
  // Pages after the first, appended in order - the shape History/Transactions.tsx
  // uses for "muat lebih banyak".
  const [more, setMore] = useState<MembersPage | null>(null)
  const [moreLoading, setMoreLoading] = useState(false)
  const [moreError, setMoreError] = useState<ApiError | null>(null)
  const generation = useRef(0)

  const loadFirstPage = useCallback(async (): Promise<FirstPage> => {
    const [page, tiers] = await Promise.all([listMembersPage(q || undefined), listDuesTiers()])
    return { page, tiers }
  }, [q])

  useEffect(() => {
    generation.current += 1
    // oxlint-disable-next-line react/set-state-in-effect -- reset the appended pages whenever the first page is refetched; the refetch is the external sync
    setMore(null)
    setMoreLoading(false)
    setMoreError(null)
    void run(loadFirstPage)
  }, [run, loadFirstPage])

  // The URL changed under the field (back, forward, a link): follow it. A
  // draft that already trims to the same search is left alone, so a trailing
  // space she is still typing is not snatched away.
  useEffect(() => {
    // oxlint-disable-next-line react/set-state-in-effect -- follow the URL back into the search field (back, forward, a link); the URL is the source of truth
    setDraft((current) => (current.trim() === q ? current : q))
  }, [q])

  useEffect(() => {
    const next = draft.trim()
    if (next === q) return
    const timer = window.setTimeout(() => {
      setSearchParams(
        (current) => {
          const params = new URLSearchParams(current)
          if (next) params.set('q', next)
          else params.delete('q')
          return params
        },
        { replace: true },
      )
    }, SEARCH_DEBOUNCE_MS)
    return () => window.clearTimeout(timer)
  }, [draft, q, setSearchParams])

  function reload() {
    void run(loadFirstPage)
  }

  // After a write whose response was lost, re-read in place.
  const refresh = () => run(loadFirstPage, { silent: true })

  async function loadMore(cursor: string) {
    const startedIn = generation.current
    setMoreLoading(true)
    setMoreError(null)
    try {
      const page = await listMembersPage(q || undefined, cursor)
      if (startedIn !== generation.current) return
      setMore((prev) => ({ members: [...(prev?.members ?? []), ...page.members], nextCursor: page.nextCursor }))
    } catch (err) {
      if (startedIn !== generation.current) return
      setMoreError(err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err)))
    } finally {
      if (startedIn === generation.current) setMoreLoading(false)
    }
  }

  const members = state.data ? [...state.data.page.members, ...(more?.members ?? [])] : []
  const tiers = state.data?.tiers ?? []

  // 'member' is this screen's prefix (dialogTarget.ts). Anggota is its own
  // route, so no sibling shares `?edit=`, but the parser is still what reads
  // the param, as in History/Transactions.tsx.
  const target = parseDialogTarget('member', dialogValue)
  const editingMember = target.kind === 'edit' ? (members.find((m) => m.id === target.id) ?? null) : null

  // A `member:` value naming an id this page has not loaded (a deep link, a
  // stale link, or one already removed) - strip it once the page has loaded
  // rather than flash an empty dialog. `clear`, never `close` - a dead link is
  // not a navigation to undo (see Locations.tsx).
  useEffect(() => {
    if (target.kind === 'foreign' || target.kind === 'new') return
    if (state.status !== 'success') return
    if (target.kind === 'edit' && editingMember !== null) return
    clear()
  }, [dialogValue, target.kind, state.status, editingMember, clear])

  function renderList() {
    if (state.status === 'idle' || state.status === 'loading') {
      return <Loading />
    }

    if (state.status === 'error' || !state.data) {
      return state.error && <ErrorState error={state.error} onRetry={reload} />
    }

    const nextCursor = more ? more.nextCursor : state.data.page.nextCursor
    const emptyMessage = q ? text.noResults(q) : text.empty

    if (members.length === 0) {
      return <p className="text-muted-foreground">{emptyMessage}</p>
    }

    return (
      <>
        <ul className="flex flex-col gap-2">
          {members.map((member) => (
            <li key={member.id}>
              <button
                type="button"
                onClick={() => open(`member:${member.id}`)}
                className="flex min-h-11 w-full flex-col gap-1 rounded-lg bg-card px-4 py-3 text-left shadow-card ring-1 ring-foreground/10 select-none transition-colors hover:bg-muted/40"
              >
                <span className="sr-only">{text.editVerb}</span>{' '}
                <span className="flex w-full items-baseline justify-between gap-3">
                  <span className="min-w-0 truncate font-medium">{member.name}</span>
                </span>
                <span className="text-sm text-muted-foreground">
                  {member.tier_id === null
                    ? text.tierNone
                    : text.tierRateLine(
                        member.tier_name ?? text.tierNone,
                        member.current_rate !== null ? formatIDR(member.current_rate) : text.noRateYet,
                      )}
                </span>
                <span className="flex flex-wrap items-center gap-2">
                  {member.inactive_on !== null && <span className="text-sm text-muted-foreground">{text.inactiveBadge}</span>}
                  {/* The Tunggakan badge (ADR-032): absent entirely at
                      arrears_months === 0, a quiet row - never a zero or a
                      "lunas" reading, which belongs to copy.dues.statuses
                      and the current period alone. Terracotta, the gentle
                      semantic state for a discrepancy (Design-System.md) -
                      arrears is a normal fact to find here, not an alarm. */}
                  {member.arrears_months > 0 && (
                    <span className="tabular shrink-0 rounded-full bg-attention-soft px-2 py-0.5 text-xs font-medium text-attention-text">
                      {text.arrearsBadge(member.arrears_months)}
                    </span>
                  )}
                </span>
              </button>
            </li>
          ))}
        </ul>
        {nextCursor && moreError && <ErrorState error={moreError} onRetry={() => void loadMore(nextCursor)} />}
        {nextCursor && !moreError && (
          <Button
            type="button"
            variant="outline"
            size="lg"
            className="w-full"
            disabled={moreLoading}
            onClick={() => void loadMore(nextCursor)}
          >
            {moreLoading ? copy.common.loading : text.loadMore}
          </Button>
        )}
      </>
    )
  }

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      <div className="relative">
        <Label htmlFor="member-search" className="sr-only">
          {text.searchLabel}
        </Label>
        <Search aria-hidden="true" className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          id="member-search"
          type="search"
          autoComplete="off"
          placeholder={text.searchPlaceholder}
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          className="pl-9"
        />
      </div>

      {renderList()}

      <Button type="button" variant="outline" size="lg" className="self-start" onClick={() => open('member:new')}>
        {text.add}
      </Button>

      <AddMemberDialog
        tiers={tiers}
        open={target.kind === 'new'}
        onClose={close}
        onAdded={() => {
          close()
          reload()
        }}
        refresh={refresh}
      />
      <EditMemberDialog
        member={editingMember}
        tiers={tiers}
        open={target.kind === 'edit' && editingMember !== null}
        onClose={close}
        onChanged={() => {
          close()
          reload()
        }}
        refresh={refresh}
      />
    </section>
  )
}

function tierName(tiers: DuesTier[], tierId: number | null): string {
  if (tierId === null) return text.tierNone
  return tiers.find((tier) => tier.id === tierId)?.name ?? text.tierNone
}

/** The tier, as the same themed Select in both dialogs. */
function TierField({
  id,
  tiers,
  tierId,
  disabled,
  onChange,
}: {
  id: string
  tiers: DuesTier[]
  tierId: number | null
  disabled?: boolean
  onChange: (tierId: number | null) => void
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{text.tierLabel}</Label>
      <Select
        value={tierId === null ? NO_TIER : String(tierId)}
        onValueChange={(next) => onChange(next === NO_TIER ? null : Number(next))}
        disabled={disabled}
      >
        <SelectTrigger id={id} aria-label={text.tierLabel}>
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
  )
}

/** Adding a member after setup - the wizard's optional step, available for
 * the rest of the fund's life. joined_on defaults to today: a fund's history
 * starts at adoption, so a new member owes from now, and backdating is the
 * deliberate exception for arrears actually owed. */
function AddMemberDialog({
  tiers,
  open,
  onClose,
  onAdded,
  refresh,
}: {
  tiers: DuesTier[]
  open: boolean
  onClose: () => void
  onAdded: () => void
  refresh: () => Promise<void>
}) {
  const [state, run] = useApi<Member>()
  const [name, setName] = useState('')
  const [tierId, setTierId] = useState<number | null>(null)
  const [joinedOn, setJoinedOn] = useState(todayISODate)

  const busy = state.status === 'loading'

  // A fresh form every time the dialog opens, so a member added a moment ago
  // does not leave their name in the fields for the next one.
  useResetWhen(open, () => {
    if (open) {
      setName('')
      setTierId(null)
      setJoinedOn(todayISODate())
    }
  })

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    const trimmed = name.trim()
    if (trimmed === '') return
    void run(
      async () => {
        const created = await createMember(trimmed, tierId, joinedOn)
        onAdded()
        return created
      },
      { settle: refresh },
    )
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{text.add}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-member-name">{text.nameLabel}</Label>
            <Input id="new-member-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          <TierField id="new-member-tier" tiers={tiers} tierId={tierId} onChange={setTierId} />
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-member-joined">{text.joinedOnLabel}</Label>
            <DateField id="new-member-joined" value={joinedOn} onChange={setJoinedOn} bounds={dateBounds.membership()} />
          </div>
          {state.status === 'error' && state.error && <ErrorState error={state.error} />}
          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" size="lg" disabled={busy} onClick={onClose}>
              {text.cancel}
            </Button>
            <Button type="submit" size="lg" disabled={busy || name.trim() === ''}>
              {busy ? text.adding : text.add}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Which of the dialog's two destructive actions the footer is confirming, if
 * either - inline, in place, never a second dialog and never
 * `window.confirm()` (ADR-032), as Locations.tsx's EditLocationDialog does. */
type ConfirmKind = 'deactivate' | 'delete' | null

/**
 * Rename, retire and delete, all in one dialog. `member` is null only while
 * closing (the row that opened it may already be gone from the loaded page by
 * the time the exit animation plays) - the last known member stays on screen
 * for that window rather than blanking the form mid-close.
 */
function EditMemberDialog({
  member,
  tiers,
  open,
  onClose,
  onChanged,
  refresh,
}: {
  member: Member | null
  tiers: DuesTier[]
  open: boolean
  onClose: () => void
  onChanged: () => void
  refresh: () => Promise<void>
}) {
  const lastMemberRef = useRef<Member | null>(null)
  if (member) lastMemberRef.current = member
  const shown = member ?? lastMemberRef.current

  const [state, run] = useApi<unknown>()
  const [name, setName] = useState(shown?.name ?? '')
  const [tierId, setTierId] = useState<number | null>(shown?.tier_id ?? null)
  const [joinedOn, setJoinedOn] = useState(shown?.joined_on ?? '')
  // Null until she picks a month: the field then follows the ceiling (the
  // current month, or the join month if later), which moves with joinedOn.
  const [tierFromPick, setTierFromPick] = useState<string | null>(null)
  const [confirming, setConfirming] = useState<ConfirmKind>(null)

  const busy = state.status === 'loading'
  // "Mulai bulan" belongs to a tier change, so it appears only when the tier
  // differs from the one the member has now. Its bounds read the join date as
  // edited, because the server reads the join month after this same save.
  const tierChanged = shown !== null && tierId !== shown.tier_id
  const tierBounds = periodBounds.tierStart(joinedOn)
  const tierFrom = tierFromPick !== null && tierFromPick >= tierBounds.min && tierFromPick <= tierBounds.max ? tierFromPick : tierBounds.max
  const inactive = shown?.inactive_on !== null && shown?.inactive_on !== undefined

  // A fresh copy of the member's fields each time the dialog opens for it, and
  // the confirm footer starts closed - reopening never shows a stale edit or a
  // confirm left mid-flight.
  useEffect(() => {
    if (open && shown) {
      setName(shown.name)
      setTierId(shown.tier_id)
      setJoinedOn(shown.joined_on)
      setTierFromPick(null)
      setConfirming(null)
    }
    // oxlint-disable-next-line react/exhaustive-deps -- seed once per open, keyed on the record's id; a refetched copy of the same record would wipe the treasurer's edits
  }, [open, shown?.id])

  // onChanged closes the dialog, so it runs only once the request has
  // succeeded. useApi's run never throws - a 409 lands in `state` - and
  // closing on it would hide the deleteRefused sentence she needs to see.
  async function submit(fn: () => Promise<unknown>) {
    await run(
      async () => {
        const result = await fn()
        onChanged()
        return result
      },
      { settle: refresh },
    )
  }

  function handleSave(event: FormEvent) {
    event.preventDefault()
    if (!shown || confirming !== null) return
    const trimmed = name.trim()
    if (trimmed === '') {
      onClose()
      return
    }
    // Only what changed goes on the wire: an absent key means "leave alone"
    // server-side, an explicit null means "clear it" - how a member's tier is
    // dropped. The joined-on date only ever moves: DateField has no empty
    // state to send.
    if (trimmed === shown.name && tierId === shown.tier_id && joinedOn === shown.joined_on) {
      onClose()
      return
    }
    void submit(() =>
      updateMember(shown.id, {
        name: trimmed === shown.name ? undefined : trimmed,
        tierId: tierChanged ? tierId : undefined,
        tierEffectiveFrom: tierChanged ? tierFrom : undefined,
        joinedOn: joinedOn === shown.joined_on ? undefined : joinedOn,
      }),
    )
  }

  if (!shown) return null

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent
        closeLabel={copy.common.close}
        // As at this prop in Locations.tsx: most visits here are to deactivate
        // or delete, so focus lands on the dialog rather than the name field,
        // which would select its text and raise the keyboard unasked.
        onOpenAutoFocus={(event) => {
          event.preventDefault()
          ;(event.currentTarget as HTMLElement).focus()
        }}
      >
        <DialogHeader>
          <DialogTitle>{text.editTitle}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSave} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="edit-member-name">{text.nameLabel}</Label>
            <Input
              id="edit-member-name"
              type="text"
              value={name}
              disabled={confirming !== null}
              onChange={(event) => setName(event.target.value)}
            />
          </div>
          <TierField id="edit-member-tier" tiers={tiers} tierId={tierId} disabled={confirming !== null} onChange={setTierId} />
          {tierChanged && (
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="edit-member-tier-from">{text.tierFromLabel}</Label>
              <MonthField
                id="edit-member-tier-from"
                value={tierFrom}
                onChange={setTierFromPick}
                bounds={tierBounds}
                disabled={confirming !== null}
              />
            </div>
          )}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="edit-member-joined">{text.joinedOnLabel}</Label>
            <DateField
              id="edit-member-joined"
              value={joinedOn}
              onChange={setJoinedOn}
              bounds={dateBounds.membership()}
              disabled={confirming !== null}
            />
          </div>

          {confirming === null ? (
            <div className="flex flex-wrap gap-2 border-t border-border pt-3">
              {inactive ? (
                <Button
                  type="button"
                  variant="outline"
                  size="lg"
                  disabled={busy}
                  onClick={() => void submit(() => updateMember(shown.id, { inactiveOn: null }))}
                >
                  {busy ? text.reinstating : text.reinstate}
                </Button>
              ) : (
                <Button type="button" variant="outline" size="lg" onClick={() => setConfirming('deactivate')}>
                  {text.deactivate}
                </Button>
              )}
              <Button type="button" variant="ghost" size="lg" className="text-destructive" onClick={() => setConfirming('delete')}>
                {text.delete}
              </Button>
            </div>
          ) : (
            <p className="border-t border-border pt-3 text-sm text-attention-text">
              {confirming === 'deactivate' ? text.deactivateConfirm : text.deleteConfirm}
            </p>
          )}

          {/* The 409 gets its own sentence rather than the shared error
              copy: "sudah punya catatan - nonaktifkan, bukan hapus" tells
              her what to do next. Everything else falls through to
              ErrorState. */}
          {state.status === 'error' &&
            state.error &&
            (errorCode(state.error) === 'referenced_by_other_records' ? (
              <p role="alert" className="text-sm text-attention-text">
                {text.deleteRefused}
              </p>
            ) : (
              <ErrorState error={state.error} />
            ))}

          <DialogFooter className="mt-1">
            {confirming === null ? (
              <>
                <Button type="button" variant="outline" size="lg" disabled={busy} onClick={onClose}>
                  {text.cancel}
                </Button>
                <Button type="submit" size="lg" disabled={busy}>
                  {busy ? text.saving : text.save}
                </Button>
              </>
            ) : (
              <>
                <Button type="button" variant="outline" size="lg" disabled={busy} onClick={() => setConfirming(null)}>
                  {text.cancel}
                </Button>
                <Button
                  type="button"
                  variant={confirming === 'delete' ? 'destructive' : 'default'}
                  size="lg"
                  disabled={busy}
                  onClick={() =>
                    void submit(() =>
                      confirming === 'deactivate' ? updateMember(shown.id, { inactiveOn: todayISODate() }) : deleteMember(shown.id),
                    )
                  }
                >
                  {confirming === 'deactivate'
                    ? busy
                      ? text.deactivating
                      : text.deactivateConfirmAction
                    : busy
                      ? text.deleting
                      : text.deleteConfirmAction}
                </Button>
              </>
            )}
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
