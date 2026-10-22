import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import ErrorState from '@/components/states/ErrorState'
import TimeZoneField from '@/components/TimeZoneField'
import { copy } from '@/copy/id'
import { updateFund } from '@/lib/setup'
import { DEFAULT_TIME_ZONE, isTimeZone } from '@/lib/timeZones'
import { useApi } from '@/lib/useApi'
import { useResetWhen } from '@/lib/useResetWhen'
import type { Fund } from '@/lib/setup'

const text = copy.settings.fund

/**
 * The fund's time-zone dialog (ADR-041), opened at `?edit=fund:time_zone`. Direct
 * CRUD like the rename, no confirm step (see FundName). `updateFund` hands the
 * answer's zone to the date helpers before it resolves.
 *
 * `fund` is null only while closing (the window EditFundNameDialog documents), so
 * the last known zone stays on screen rather than the field blanking.
 */
export function EditTimeZoneDialog({
  fund,
  open,
  onClose,
  onChanged,
  refresh,
}: {
  fund: Fund | null
  open: boolean
  onClose: () => void
  onChanged: (fund: Fund) => void
  refresh: () => Promise<void>
}) {
  const [state, run] = useApi<Fund>()
  const [zone, setZone] = useState<string>(DEFAULT_TIME_ZONE)

  // Reset to the fund's current zone each time the dialog opens, so a cancelled
  // edit never leaves its choice waiting for the next one.
  useResetWhen(open ? fund : null, () => {
    if (open && fund !== null) setZone(isTimeZone(fund.time_zone) ? fund.time_zone : DEFAULT_TIME_ZONE)
  })

  const busy = state.status === 'loading'
  const unchanged = fund !== null && zone === fund.time_zone

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (unchanged) return
    void run(
      async () => {
        const updated = await updateFund({ timeZone: zone })
        onChanged(updated)
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
          <DialogTitle>{text.zone.editTitle}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          <p className="text-sm text-muted-foreground">{text.zone.body}</p>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="fund-time-zone">{copy.timeZone.label}</Label>
            <TimeZoneField id="fund-time-zone" value={zone} onChange={setZone} disabled={busy} />
          </div>
          {state.status === 'error' && state.error && <ErrorState error={state.error} />}
          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" size="lg" disabled={busy} onClick={onClose}>
              {text.cancel}
            </Button>
            <Button type="submit" size="lg" disabled={busy || unchanged}>
              {busy ? text.saving : text.save}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
