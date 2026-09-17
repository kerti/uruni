import { useEffect, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import AmountInput from '@/components/money/AmountInput'
import Loading from '@/components/states/Loading'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { todayISODate } from '@/lib/dates'
import { listIncidentals, openIncidental } from '@/lib/incidentals'
import { useApi } from '@/lib/useApi'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { useDialogParam } from '@/lib/useDialogParam'
import type { Incidental } from '@/lib/incidentals'

const text = copy.settings.incidentals
const openText = copy.incidentals.open

/**
 * This section owns `?edit=incidental:new` and nothing else: there is no
 * edit-existing dialog here, because a card navigates straight to the
 * envelope's own detail view (`/incidentals?purpose=<id>`). So an
 * `incidental:<id>` that arrives by hand is `invalid` - a value with this
 * section's prefix and no dialog to show for it. See dialogTarget.ts for why
 * `foreign` is the case that matters on a screen whose sections share a URL.
 */
function parseEditTarget(value: string | null): 'new' | 'foreign' | 'invalid' {
  const target = parseDialogTarget('incidental', value)
  if (target.kind === 'edit') return 'invalid'
  return target.kind
}

/**
 * The incidentals section of the settings screen (#263, ADR-032 "an
 * incidental is a purpose, so it is opened in Pengaturan"). Opening a new
 * envelope lost its home when M6.33 (#265) turned Beranda's purpose
 * breakdown into entry points only - this section is where it lands
 * instead, beside Titipan, because CONTEXT.md makes incidental and
 * pass-through two kinds of one `purpose`.
 *
 * Every envelope the fund has opened is listed here, closed ones included -
 * the same "retired isn't hidden" choice Locations makes, and the reason is
 * the same too: reopening (ADR-031) needs a door that does not wait on a
 * purpose filter (#262). Tapping a card navigates to its detail view; only
 * "open a new envelope" is a dialog here, addressed by
 * `?edit=incidental:new`, same idiom as Locations' `?edit=location:new`.
 *
 * The open form itself is Incidentals.tsx's former OpenForm, lifted here
 * nearly verbatim: occasion, an optional target, and a date defaulting to
 * today. Contributing and disbursing stay on the envelope's own detail
 * screen, reached by tapping the card once it is open.
 */
export default function SettingsIncidentals() {
  const navigate = useNavigate()
  const [listState, listRun] = useApi<Incidental[]>()
  const { value, open, close, clear } = useDialogParam()

  useEffect(() => {
    // listRun is a stable useCallback (useApi.ts), so this fires once.
    void listRun(() => listIncidentals(false))
  }, [listRun])

  function reload() {
    void listRun(() => listIncidentals(false))
  }

  const target = parseEditTarget(value)

  // `incidental:` naming anything but a new envelope: strip it once the list
  // has loaded, rather than flash an empty dialog. Only ever a value this
  // section owns - a `foreign` one belongs to a sibling section and is never
  // touched. clear(), never close() - same reasoning as Locations.tsx: a dead
  // link is not a real navigation to undo, and a reload racing a close()'s own
  // back navigation must not leave the settings screen.
  useEffect(() => {
    if (target !== 'invalid') return
    if (listState.status !== 'success') return
    clear()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, listState.status])

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      {listState.status === 'idle' || listState.status === 'loading' ? (
        <Loading />
      ) : listState.status === 'error' || !listState.data ? (
        listState.error && <ErrorState error={listState.error} onRetry={reload} />
      ) : listState.data.length === 0 ? (
        <p className="text-muted-foreground">{text.empty}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {listState.data.map((envelope) => (
            <li key={envelope.purpose_id}>
              <button
                type="button"
                aria-label={text.cardAria(envelope.occasion)}
                onClick={() => navigate(`/incidentals?purpose=${envelope.purpose_id}`)}
                className="flex min-h-11 w-full items-center justify-between gap-3 rounded-lg bg-card px-4 py-3 text-left ring-1 ring-foreground/10 select-none transition-colors hover:bg-muted/40"
              >
                <span className="min-w-0 truncate font-medium">{envelope.occasion}</span>
                <StatusBadge envelope={envelope} />
              </button>
            </li>
          ))}
        </ul>
      )}

      <Button type="button" variant="outline" className="h-11 self-start" onClick={() => open('incidental:new')}>
        {text.add}
      </Button>

      <OpenIncidentalDialog
        open={target === 'new'}
        onClose={close}
        onOpened={() => {
          close()
          reload()
        }}
      />
    </section>
  )
}

/** Same open/closed badge Incidentals.tsx's own StatusBadge renders. Kept
 * as a small local copy rather than a shared export - one component, two
 * call sites, is not yet worth a shared file. */
function StatusBadge({ envelope }: { envelope: Incidental }) {
  if (envelope.closed_on) {
    return <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">{copy.incidentals.status.closed}</span>
  }
  return <span className="shrink-0 rounded-full bg-success-soft px-2 py-0.5 text-xs font-medium text-success">{copy.incidentals.status.open}</span>
}

/** Opening a new envelope, in the dialog primitive (ADR-032). The fields
 * and their copy carry over unchanged from Incidentals.tsx's former
 * OpenForm - occasion doubles as the purpose's own name, target is
 * optional, and the date defaults to today. */
function OpenIncidentalDialog({ open, onClose, onOpened }: { open: boolean; onClose: () => void; onOpened: () => void }) {
  const [state, run] = useApi<Incidental>()
  const [occasion, setOccasion] = useState('')
  const [targetAmount, setTargetAmount] = useState(0)
  const [openedOn, setOpenedOn] = useState(todayISODate())

  const busy = state.status === 'loading'
  const canSubmit = occasion.trim() !== '' && openedOn !== '' && !busy

  // A fresh form every time the dialog opens, so an envelope opened a
  // moment ago does not leave its occasion sitting in the fields for the
  // next one.
  useEffect(() => {
    if (open) {
      setOccasion('')
      setTargetAmount(0)
      setOpenedOn(todayISODate())
    }
  }, [open])

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (!canSubmit) return
    void run(async () => {
      const opened = await openIncidental({
        occasion: occasion.trim(),
        targetAmount: targetAmount > 0 ? targetAmount : null,
        openedOn,
      })
      onOpened()
      return opened
    })
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
          <DialogTitle>{openText.heading}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-incidental-occasion">{openText.occasionLabel}</Label>
            <Input
              id="new-incidental-occasion"
              type="text"
              placeholder={openText.occasionPlaceholder}
              value={occasion}
              onChange={(event) => setOccasion(event.target.value)}
              disabled={busy}
            />
          </div>

          <AmountInput
            id="new-incidental-target"
            label={openText.targetLabel}
            value={targetAmount}
            onChange={setTargetAmount}
            disabled={busy}
          />

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-incidental-opened">{openText.dateLabel}</Label>
            <Input
              id="new-incidental-opened"
              type="date"
              value={openedOn}
              onChange={(event) => setOpenedOn(event.target.value)}
              disabled={busy}
            />
          </div>

          {state.status === 'error' && state.error && <ErrorState error={state.error} />}

          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={onClose}>
              {openText.cancel}
            </Button>
            <Button type="submit" className="h-11" disabled={!canSubmit}>
              {busy ? openText.submitting : openText.submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
