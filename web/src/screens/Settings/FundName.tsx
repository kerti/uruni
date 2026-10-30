import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { updateFund } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import { isTimeZone, DEFAULT_TIME_ZONE } from '@/lib/timeZones'
import { useDialogParam } from '@/lib/useDialogParam'
import { useResetWhen } from '@/lib/useResetWhen'
import type { Fund } from '@/lib/setup'
import { EditTimeZoneDialog } from '@/screens/Settings/FundTimeZone'

const text = copy.settings.fund

/**
 * The kas's own settings, "Kas ini": its name and its time zone, each a card-plus-dialog
 * (ADR-032) at `?edit=fund:name` / `?edit=fund:time_zone` (ADR-041). The setup wizard
 * promises both - "bisa diganti nanti kalau perlu".
 *
 * The name is a display label: it heads every screen and the public report,
 * and nothing posted references it, so a rename rewrites no history. It does
 * not touch the report's slug, the address she may have shared; replacing that
 * is its own decision (Laporan publik's "Buat tautan baru"), not a side effect
 * of fixing a typo.
 *
 * A zone change rewrites nothing either (ADR-041): every day and month is read
 * from it, so setting the old zone back restores every earlier reading, and
 * there is no confirm step.
 *
 * The fund is App.tsx's, read once at boot and passed down rather than fetched
 * again here. `onChanged` hands a saved fund back to it, which updates Shell's
 * header and this section together; `refresh` re-reads it after a write whose
 * outcome is unknown.
 *
 * There is exactly one fund, so the dialogs carry no id: `fund:name` and
 * `fund:time_zone` are the only values; any other `?edit=fund:<x>` is cleared once.
 */
export default function FundName({
  fund,
  onChanged,
  refresh,
}: {
  fund: Fund
  onChanged: (fund: Fund) => void
  refresh: () => Promise<void>
}) {
  const { value, open, close, clear } = useDialogParam()

  // 'fund' is this section's prefix (dialogTarget.ts). The rename dialog is
  // addressed by name rather than id, so it lands as `invalid` on that parser -
  // this section's one owned value, told apart from a real dead link by
  // comparing the raw string.
  const target = parseDialogTarget('fund', value)
  const isOwn = target.kind !== 'foreign'
  const editing = value === 'fund:name'
  const editingZone = value === 'fund:time_zone'

  // A `fund:` value this section cannot use: strip it once, rather than leave a
  // param no dialog answers to. Never a `foreign` value - that belongs to a
  // sibling section. `clear`, not `close`, for the reason Locations documents.
  useEffect(() => {
    if (!isOwn || editing || editingZone) return
    clear()
  }, [value, isOwn, editing, editingZone, clear])

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      <div className="flex flex-col gap-2">
        <SettingCard verb={text.editVerb} label={text.nameLabel} value={fund.name} onClick={() => open('fund:name')} />
        <SettingCard
          verb={text.zone.editVerb}
          label={copy.timeZone.label}
          value={copy.timeZone.options[isTimeZone(fund.time_zone) ? fund.time_zone : DEFAULT_TIME_ZONE].label}
          onClick={() => open('fund:time_zone')}
        />
      </div>

      <EditFundNameDialog
        fund={fund}
        open={editing}
        onClose={close}
        onRenamed={(updated) => {
          onChanged(updated)
          close()
        }}
        refresh={refresh}
      />
      <EditTimeZoneDialog
        fund={fund}
        open={editingZone}
        onClose={close}
        onChanged={(updated) => {
          onChanged(updated)
          close()
        }}
        refresh={refresh}
      />
    </section>
  )
}

/** One setting as a card: its label small above its value, the whole card the
 * edit affordance. */
function SettingCard({ verb, label, value, onClick }: { verb: string; label: string; value: string; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex min-h-11 w-full flex-col items-start rounded-lg bg-card px-4 py-3 text-left shadow-card ring-1 ring-foreground/10 select-none transition-colors hover:bg-muted/40"
    >
      {/* The verb is visually hidden text, first in the button, so the
          accessible name is built from the content and holds every visible
          word (WCAG 2.5.3) - an aria-label would replace it. */}
      <span className="sr-only">{verb}</span> <span className="text-xs text-muted-foreground">{label}</span>
      <span className="w-full min-w-0 truncate font-medium">{value}</span>
    </button>
  )
}

/**
 * The rename dialog. `fund` is null only while closing (the window
 * EditLocationDialog documents), so the last known name stays on screen
 * rather than the field blanking mid-close.
 */
function EditFundNameDialog({
  fund,
  open,
  onClose,
  onRenamed,
  refresh,
}: {
  fund: Fund | null
  open: boolean
  onClose: () => void
  onRenamed: (fund: Fund) => void
  refresh: () => Promise<void>
}) {
  const [state, run] = useApi<Fund>()
  const [name, setName] = useState('')

  // Reset to the fund's current name each time the dialog opens, so a cancelled
  // edit never leaves its text waiting for the next one.
  useResetWhen(open ? fund : null, () => {
    if (open && fund !== null) setName(fund.name)
  })

  const busy = state.status === 'loading'
  const trimmed = name.trim()
  const unchanged = fund !== null && trimmed === fund.name

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (trimmed === '' || unchanged) return
    void run(
      async () => {
        const updated = await updateFund({ name: trimmed })
        onRenamed(updated)
        return updated
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
          <DialogTitle>{text.editTitle}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="fund-name">{text.nameLabel}</Label>
            <Input id="fund-name" type="text" value={name} onChange={(event) => setName(event.target.value)} />
          </div>
          {state.status === 'error' && state.error && <ErrorState error={state.error} />}
          <DialogFooter className="mt-1">
            <Button type="submit" size="lg" disabled={busy || trimmed === '' || unchanged}>
              {busy ? text.saving : text.save}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
