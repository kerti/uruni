import { useEffect, useState, type FormEvent } from 'react'

import DateField from '@/components/DateField'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import AmountInput from '@/components/money/AmountInput'
import { MemberMultiPicker } from '@/components/pickers/MemberPicker'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { todayISODate, dateBounds } from '@/lib/dates'
import { openIncidental } from '@/lib/incidentals'
import { listAllMembers } from '@/lib/setup'
import { useApi } from '@/lib/useApi'
import { useResetWhen } from '@/lib/useResetWhen'
import type { Incidental } from '@/lib/incidentals'
import type { Member } from '@/lib/setup'

const openText = copy.incidentals.open
const descriptionId = 'open-incidental-description'

/** What Buka amplop hands the new amplop's screen: App.tsx's arrival toast
 * reads it and says the amplop is open. */
export interface IncidentalOpenedState {
  incidentalOpened: true
}

/**
 * Opening a new envelope, in the dialog primitive (ADR-032), hosted by Beranda
 * (ADR-047): occasion doubles as the purpose's name, target is optional, and
 * the date defaults to today. The description under the title is the only
 * place the app says what an amplop is - read the moment she opens one.
 *
 * The recipients picker's roster (ADR-034) is fetched when the dialog opens,
 * not with Beranda's own load: most visits to Beranda never open it.
 */
export default function OpenIncidentalDialog({
  open,
  onClose,
  onOpened,
}: {
  open: boolean
  onClose: () => void
  onOpened: (opened: Incidental) => void
}) {
  const [state, run] = useApi<Incidental>()
  const [membersState, membersRun] = useApi<Member[]>()
  const [occasion, setOccasion] = useState('')
  const [targetAmount, setTargetAmount] = useState(0)
  const [openedOn, setOpenedOn] = useState(todayISODate())
  const [minimumPerMember, setMinimumPerMember] = useState(0)
  const [recipientMemberIds, setRecipientMemberIds] = useState<number[]>([])

  const busy = state.status === 'loading'
  const canSubmit = occasion.trim() !== '' && openedOn !== '' && !busy

  useEffect(() => {
    // membersRun is a stable useCallback (useApi.ts): once per opening.
    if (open) void membersRun(listAllMembers)
  }, [open, membersRun])

  // A fresh form every time the dialog opens, so an envelope opened a moment
  // ago does not leave its occasion in the fields for the next one.
  useResetWhen(open, () => {
    if (open) {
      setOccasion('')
      setTargetAmount(0)
      setOpenedOn(todayISODate())
      setMinimumPerMember(0)
      setRecipientMemberIds([])
    }
  })

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (!canSubmit) return
    void run(async () => {
      const opened = await openIncidental({
        occasion: occasion.trim(),
        targetAmount: targetAmount > 0 ? targetAmount : null,
        openedOn,
        minimumPerMember: minimumPerMember > 0 ? minimumPerMember : null,
        recipientMemberIds,
      })
      onOpened(opened)
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
      <DialogContent closeLabel={copy.common.close} aria-describedby={descriptionId}>
        <DialogHeader>
          <DialogTitle>{openText.heading}</DialogTitle>
          <DialogDescription id={descriptionId}>{openText.description}</DialogDescription>
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
            <DateField id="new-incidental-opened" value={openedOn} onChange={setOpenedOn} bounds={dateBounds.entry()} disabled={busy} />
          </div>

          {/* ADR-034 (#211): one figure every expected member is asked to
              give, and who the envelope is for - both optional, both
              editable later through the same fields on the edit dialog
              (Incidentals.tsx's own RenameIncidentalDialog). */}
          <AmountInput
            id="new-incidental-minimum"
            label={openText.minimumLabel}
            value={minimumPerMember}
            onChange={setMinimumPerMember}
            disabled={busy}
          />

          <MemberMultiPicker
            label={openText.recipientsLabel}
            members={membersState.data ?? []}
            value={recipientMemberIds}
            onChange={setRecipientMemberIds}
            emptyMessage={openText.recipientsEmpty}
            disabled={busy}
          />

          {state.status === 'error' && state.error && <ErrorState error={state.error} />}

          <DialogFooter className="mt-1">
            <Button type="submit" size="lg" disabled={!canSubmit}>
              {busy ? openText.submitting : openText.submit}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
