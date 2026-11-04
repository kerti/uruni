import { useCallback, useEffect, useState } from 'react'

import RecordSummary from '@/components/RecordSummary'
import ErrorState from '@/components/states/ErrorState'
import Loading from '@/components/states/Loading'
import { cardSurface } from '@/components/cardClasses'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import { currentPeriod, formatPeriod } from '@/lib/dates'
import { getOutstandingDues, unwaiveDues, waiveDues, type DuesStatusRow, type OutstandingDuesPeriod } from '@/lib/dues'
import { formatIDR } from '@/lib/money'
import { useApi } from '@/lib/useApi'

const text = copy.dues.waiver
const paymentText = copy.dues.payment

/** How far past the current month a waiver reaches: the payment form's year
 * ahead (ADR-050), so the two offer the same months. */
const MAX_MONTHS_AHEAD = 12

/** The YYYY-MM `months` after `period` - integer arithmetic, never a Date. */
function addMonths(period: string, months: number): string {
  const [year, month] = period.split('-').map(Number)
  const index = year * 12 + (month - 1) + months
  return `${Math.floor(index / 12)}-${String((index % 12) + 1).padStart(2, '0')}`
}

/** What a waiver of this month stops being owed: the rate less anything paid. */
function remainingOf(period: OutstandingDuesPeriod): number {
  return Math.max(period.owed_amount - period.paid_amount, 0)
}

/**
 * Putihkan iuran (ADR-050): the group's decision that a member's dues for one
 * or more months stop being owed. A one-record dialog - the member first - then
 * the payment form's checklist of what the member still owes (the same server
 * walk, GET /api/members/{id}/outstanding-dues, so the two never disagree),
 * each month with what it still owes, read-only: a waiver covers the whole
 * remainder. The card's month comes ticked; *Pilih semua tunggakan* ticks the
 * arrears; *Tambah <bulan>* reaches one more month ahead, up to a year. Moves
 * no money, so the total is information only.
 */
export function WaiveDialog({
  row,
  period,
  onClose,
  onWaived,
}: {
  /** The card being waived; null keeps the dialog closed. */
  row: DuesStatusRow | null
  /** The card's month, ticked when the list loads. */
  period: string
  onClose: () => void
  onWaived: () => void
}) {
  return (
    <Dialog
      open={row !== null}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{text.heading}</DialogTitle>
        </DialogHeader>
        {/* Mounted per opening (DialogContent unmounts on close): every
            opening reads what the member owes afresh. */}
        {row && <WaiveForm row={row} period={period} onClose={onClose} onWaived={onWaived} />}
      </DialogContent>
    </Dialog>
  )
}

function WaiveForm({ row, period, onClose, onWaived }: { row: DuesStatusRow; period: string; onClose: () => void; onWaived: () => void }) {
  const now = currentPeriod()
  const [monthsAhead, setMonthsAhead] = useState(() => {
    // A card for a month ahead must be in reach to come ticked.
    let n = 0
    while (n < MAX_MONTHS_AHEAD && addMonths(now, n) < period) n += 1
    return n
  })
  const through = addMonths(now, monthsAhead)
  const [selected, setSelected] = useState<string[]>([period])
  const [note, setNote] = useState('')
  const [owed, loadOwed] = useApi<OutstandingDuesPeriod[]>()
  const [submit, runSubmit] = useApi<void>()
  const submitting = submit.status === 'loading'

  const load = useCallback(() => loadOwed(() => getOutstandingDues(row.member.id, through)), [loadOwed, row.member.id, through])
  useEffect(() => {
    void load()
  }, [load])

  const periods = owed.data ?? []
  // Only months on offer can stay ticked: the card's month is dropped if the
  // server does not call it owed.
  const ticked = selected.filter((p) => periods.some((o) => o.period === p))
  const arrears = periods.filter((p) => p.period < now).map((p) => p.period)
  const total = periods.filter((p) => ticked.includes(p.period)).reduce((sum, p) => sum + remainingOf(p), 0)

  function toggle(p: string, checked: boolean) {
    setSelected((current) => (checked ? [...current, p] : current.filter((x) => x !== p)))
  }

  function handleSubmit() {
    const trimmed = note.trim()
    void runSubmit(async () => {
      await waiveDues(row.member.id, [...ticked].sort(), trimmed === '' ? null : trimmed)
      onWaived()
    })
  }

  return (
    <>
      {/* The record is the member and the months below, each with what it
          still owes - a kategori would only repeat the amounts. */}
      <RecordSummary items={[{ label: text.memberLabel, value: row.member.name }]} />

      <div className="flex flex-col gap-1.5">
        <span id="waive-months" className="text-xs leading-none font-medium text-muted-foreground">
          {text.monthsLabel}
        </span>
        {(owed.status === 'idle' || owed.status === 'loading') && <Loading />}
        {owed.status === 'error' && owed.error && <ErrorState error={owed.error} onRetry={() => void load()} />}
        {owed.status === 'success' &&
          (periods.length === 0 ? (
            <p className="text-muted-foreground">{paymentText.noOutstanding}</p>
          ) : (
            <div className="flex flex-col gap-1">
              {arrears.length > 0 && (
                <Button
                  type="button"
                  variant="link"
                  size="sm"
                  className="self-start px-0"
                  disabled={submitting}
                  onClick={() => setSelected((current) => [...current, ...arrears.filter((p) => !current.includes(p))])}
                >
                  {paymentText.selectAllArrears}
                </Button>
              )}
              <ul aria-labelledby="waive-months" className={`flex flex-col divide-y divide-foreground/10 ${cardSurface}`}>
                {periods.map((p) => (
                  <li key={p.period} className="flex min-h-11 items-center gap-2 px-4">
                    <label className="flex min-h-11 flex-1 items-center gap-2 font-medium">
                      <input
                        type="checkbox"
                        className="size-4 rounded border-input"
                        checked={ticked.includes(p.period)}
                        onChange={(event) => toggle(p.period, event.target.checked)}
                        disabled={submitting}
                      />
                      <span>{formatPeriod(p.period)}</span>
                      {p.status === 'partial' && (
                        <span className="text-sm font-normal text-muted-foreground">
                          {'\u00b7'} {paymentText.partialSuffix}
                        </span>
                      )}
                    </label>
                    <span className="tabular text-muted-foreground">{formatIDR(remainingOf(p))}</span>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        {owed.status === 'success' && monthsAhead < MAX_MONTHS_AHEAD && (
          <Button type="button" variant="outline" size="lg" disabled={submitting} onClick={() => setMonthsAhead((n) => n + 1)}>
            {paymentText.addMonth(formatPeriod(addMonths(through, 1)))}
          </Button>
        )}
      </div>

      {ticked.length > 0 && (
        <p className="tabular flex items-center justify-between font-medium">
          <span>{text.totalLabel}</span>
          <span>{formatIDR(total)}</span>
        </p>
      )}

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="waive-note">{text.noteLabel}</Label>
        <textarea
          id="waive-note"
          rows={2}
          className="w-full rounded-lg border border-input bg-transparent px-2.5 py-1.5 text-base outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-50 md:text-sm"
          value={note}
          onChange={(event) => setNote(event.target.value)}
          disabled={submitting}
        />
      </div>

      {submit.status === 'error' && submit.error && <ErrorState error={submit.error} />}

      <DialogFooter className="mt-1">
        <Button type="button" variant="outline" size="lg" disabled={submitting} onClick={onClose}>
          {copy.common.close}
        </Button>
        <Button type="button" size="lg" disabled={ticked.length === 0 || submitting} onClick={handleSubmit}>
          {submitting ? text.submitting : text.submit}
        </Button>
      </DialogFooter>
    </>
  )
}

/**
 * Batalkan pemutihan: a confirm on one record (ADR-046), showing the member,
 * the month and her note, so she knows exactly which waiver she is taking back.
 * The month is owed again, less anything already paid toward it.
 */
export function UnwaiveDialog({
  row,
  period,
  onClose,
  onUnwaived,
}: {
  row: DuesStatusRow | null
  period: string
  onClose: () => void
  onUnwaived: () => void
}) {
  const [state, run] = useApi<void>()
  const busy = state.status === 'loading'

  function handleConfirm() {
    if (!row) return
    void run(async () => {
      await unwaiveDues(row.member.id, period)
      onUnwaived()
    })
  }

  return (
    <Dialog
      open={row !== null}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
    >
      <DialogContent closeLabel={copy.common.close}>
        <DialogHeader>
          <DialogTitle>{text.undoHeading}</DialogTitle>
        </DialogHeader>
        {row && (
          <RecordSummary
            items={[
              { label: text.memberLabel, value: row.member.name },
              { label: text.undoMonthLabel, value: formatPeriod(period) },
              ...(row.waiver_note ? [{ label: text.undoNoteLabel, value: row.waiver_note, block: true }] : []),
            ]}
          />
        )}
        <p className="text-sm text-muted-foreground">{text.undoBody}</p>
        {state.status === 'error' && state.error && <ErrorState error={state.error} />}
        <DialogFooter className="mt-1">
          {/* A take-back, worded as a reversal's confirm is (#742): Tidak jadi
              beside Ya, batalkan. */}
          <Button type="button" variant="outline" size="lg" disabled={busy} onClick={onClose}>
            {copy.dues.history.cancel}
          </Button>
          <Button type="button" size="lg" disabled={busy} onClick={handleConfirm}>
            {busy ? text.undoing : text.undoConfirm}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
