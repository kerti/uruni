import { useEffect, useState, type FormEvent } from 'react'

import PurposePicker from '@/components/pickers/PurposePicker'
import ErrorState from '@/components/states/ErrorState'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { copy } from '@/copy/id'
import { correctPurpose } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { Purpose } from '@/lib/purposes'
import type { Transaction } from '@/lib/setup'

const text = copy.purposeCorrection

/**
 * Correcting one posted row's peruntukan (#276, ADR-033).
 *
 * One field, because everything else is read off the row server-side and
 * cannot be sent: the amount, the account and the date are the original's,
 * which is what keeps this a correction of that row rather than an edit of
 * an immutable entry. It is a dialog rather than a route under ADR-032's
 * sharpened rule - a dialog may post only when the posting is value-neutral
 * and carries a single field - and the pair it posts cannot move a balance
 * in any month, so the worst a stray backdrop tap can do is nothing.
 *
 * `currentLabel` appears only on a row that has already been corrected,
 * where the money is under a tag the row itself no longer shows. Without it
 * the dialog and the row beneath it would appear to disagree: the list
 * renders the STORED tag deliberately, because the ledger sums stored tags
 * and a row showing its effective one would put the screen out of step with
 * the balances (ADR-033). The server builds the pair from the effective tag
 * either way; this is only saying so.
 *
 * `transaction` is null only while the dialog closes, the same window
 * EditPassThroughDialog documents for its own row prop.
 */
export default function CorrectPurposeDialog({
  transaction,
  purposes,
  purposeNames,
  open,
  onClose,
  onCorrected,
}: {
  transaction: Transaction | null
  purposes: Purpose[]
  purposeNames: Map<number, string>
  open: boolean
  onClose: () => void
  onCorrected: () => void
}) {
  const [state, run] = useApi<unknown>()
  const [purposeId, setPurposeId] = useState<number | null>(null)

  // The effective tag - where the money actually is - which is the row's own
  // peruntukan until a correction has moved it.
  const effectivePurposeId = transaction ? (transaction.effective_purpose_id ?? transaction.purpose_id) : null
  const alreadyCorrected = transaction !== null && effectivePurposeId !== transaction.purpose_id

  // A fresh picker every time the dialog opens, seeded with where the money
  // is now - so the field starts on a true statement, and the submit button
  // starts disabled until she actually changes something.
  useEffect(() => {
    if (open && transaction !== null) setPurposeId(effectivePurposeId)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, transaction])

  const busy = state.status === 'loading'
  const unchanged = purposeId === null || purposeId === effectivePurposeId

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    if (transaction === null || purposeId === null || unchanged) return
    void run(async () => {
      const result = await correctPurpose(transaction.id, purposeId)
      onCorrected()
      return result
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
          <DialogTitle>{text.heading}</DialogTitle>
        </DialogHeader>
        <form className="flex flex-col gap-3" onSubmit={handleSubmit} noValidate>
          {alreadyCorrected && effectivePurposeId !== null && (
            <p className="text-sm text-muted-foreground">
              {text.currentLabel(purposeNames.get(effectivePurposeId) ?? copy.home.purposeUnknown)}
            </p>
          )}

          <PurposePicker
            id="correct-purpose"
            label={text.pickerLabel}
            purposes={purposes}
            value={purposeId}
            onChange={setPurposeId}
            disabled={busy}
          />

          {/* Said before she taps, not after: this is the one action in the
              app that posts rows without moving money, and a treasurer who
              is anxious about the numbers should not have to infer that. */}
          <p className="text-sm text-muted-foreground">{text.explainer}</p>

          {state.status === 'error' && state.error && <ErrorState error={state.error} />}

          <DialogFooter className="mt-1">
            <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={onClose}>
              {text.cancel}
            </Button>
            <Button type="submit" className="h-11" disabled={busy || unchanged}>
              {busy ? text.saving : text.save}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
