import { Camera, PenLine } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'

import ReceiptDialog from '@/components/ReceiptDialog'
import ReversalDateLine from '@/components/ReversalDateLine'
import TransactionDetailDialog from '@/components/TransactionDetailDialog'
import { transferHeadline, transferKind } from '@/components/rowLabel'
import TransactionGlyph from '@/components/TransactionGlyph'
import TransactionRowLabel from '@/components/TransactionRowLabel'
import { Button } from '@/components/ui/button'
import { useToast } from '@/components/toast/useToast'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { noteForDisplay, reverseTransaction } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { listPurposes } from '@/lib/purposes'
import { useDialogParam } from '@/lib/useDialogParam'
import CorrectPurposeDialog from '@/components/CorrectPurposeDialog'
import type { Purpose } from '@/lib/purposes'
import type { Transaction } from '@/lib/transactions'

const reverseText = copy.history.transactions

/** Wire error code -> Indonesian copy, scoped first to this list's wording
 * (transaction_already_reversed reads as "iuran" in the shared map, ADR-042)
 * then falling back to it, as Incidentals.tsx's errorText does. */
function reversalErrorText(err: ApiError): string {
  const specific = reverseText.errors[err.code as keyof typeof reverseText.errors]
  if (specific) return specific
  const common = copy.common.errors[err.code as keyof typeof copy.common.errors]
  return common ?? copy.common.unknownError
}

/**
 * One transaction row's markup, shared by Home and Riwayat's Transaksi tab
 * (Transactions.tsx). Home passes its recent five and Transactions the whole
 * list; this component has no opinion on count or order, only on how one row
 * reads.
 */
export default function TransactionList({
  transactions,
  purposeNames,
  emptyMessage,
  onRefresh,
}: {
  transactions: Transaction[]
  /** transaction.purpose_id -> its name - a row carries only the id, and
   * GET /api/balances answers with every purpose the fund has (see Home.tsx). */
  purposeNames: Map<number, string>
  emptyMessage: string
  /** Re-reads whatever the caller's `transactions` came from (and its figures:
   * a correction or a reversal moves balances). Passing it is what gives every
   * row's detail its actions - Perbaiki pos, Nota, Batalkan transaksi - so the
   * same row offers the same buttons on Beranda, in an envelope and in Riwayat
   * (#739); which of them a given row shows is per row (canCorrectPurpose, the
   * server's `reversible`, a nota or none). This component owns the correction
   * dialog (ADR-033), the reversal form (ADR-042), the receipt viewer and the
   * "done" lines, and holds no list state, so the caller only refetches. Omit it
   * and the rows are read-only. */
  onRefresh?: () => Promise<void> | void
}) {
  // The row whose photo dialog is open, by id rather than by object, so the
  // dialog reads fresh receipt_ids off the current `transactions` prop the
  // moment the caller's onRefresh resolves.
  const [receiptsForId, setReceiptsForId] = useState<number | null>(null)
  // The entry read in full, by id in the URL (ADR-032: an open dialog is a
  // search parameter), so back closes it. An id not on this page - a stale
  // deep link - opens nothing.
  const entry = useDialogParam('entry')
  // The correction dialog, `?edit=purpose-correction:<id>`. parseDialogTarget
  // tells it from the `?edit=` values of its neighbours (Riwayat's filter,
  // an envelope's rename), which it leaves alone and which leave it alone.
  const correction = useDialogParam()
  const correctionTarget = parseDialogTarget('purpose-correction', correction.value)
  const correctionKind = correctionTarget.kind
  const correctionId = correctionTarget.kind === 'edit' ? correctionTarget.id : null
  // The picker's choices: every open purpose (a closed envelope's is refused,
  // ADR-033). Fetched once the first time a detail or a correction is open, so
  // the picker is filled by the time Perbaiki pos is tapped.
  const [selectable, setSelectable] = useState<Purpose[] | null>(null)
  const wantsSelectable = onRefresh !== undefined && (entry.value !== null || correctionKind === 'edit')
  useEffect(() => {
    if (!wantsSelectable || selectable !== null) return
    let live = true
    listPurposes(true)
      .then((purposes) => {
        if (live) setSelectable(purposes)
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [wantsSelectable, selectable])
  const toast = useToast()
  // The row whose reversal form is open under it (ADR-042), if any - one at a
  // time, so the reason always belongs to a single row.
  const [reversingId, setReversingId] = useState<number | null>(null)
  // What the entry detail handed off to when it closed (Batalkan, Perbaiki pos,
  // Nota). Run from the dialog's own onCloseAutoFocus, i.e. once it is gone, so
  // two modal dialogs are never open at once: the correction dialog is a URL
  // param like the detail, and pushing it while the detail's back-navigation is
  // still in flight would race.
  const handoff = useRef<{ kind: 'reverse' | 'correct' | 'receipts'; transaction: Transaction } | null>(null)

  // A `purpose-correction:` value naming a row this list does not hold, or a
  // malformed one: strip it rather than leave an inert param in the URL.
  // clear(), never close() - a dead link is not a navigation to undo. A
  // `foreign` value belongs to something else on the screen and is left alone.
  const { clear: clearCorrection } = correction
  const knowsCorrectionRow = correctionId !== null && transactions.some((t) => t.id === correctionId)
  useEffect(() => {
    if (correctionKind === 'foreign' || correctionKind === 'new') return
    if (knowsCorrectionRow) return
    clearCorrection()
  }, [correctionKind, knowsCorrectionRow, clearCorrection])

  if (transactions.length === 0) {
    return <p className="text-muted-foreground">{emptyMessage}</p>
  }

  const receiptsForTransaction = transactions.find((t) => t.id === receiptsForId) ?? null
  const correcting = transactions.find((t) => t.id === correctionId) ?? null
  const detailTransaction = entry.value === null ? null : (transactions.find((t) => String(t.id) === entry.value) ?? null)

  // The correction dialog names a row's current pos too, which may be one this
  // list's own rows never mention (an envelope's activity knows only itself).
  const dialogNames = new Map([...(selectable ?? []).map((p): [number, string] => [p.id, p.name]), ...purposeNames])

  return (
    <>
      <ul className="flex flex-col gap-2">
        {transactions.map((transaction) => {
          const note = noteForDisplay(transaction)
          const kind = transferKind(transaction)
          const receiptCount = transaction.receipt_ids?.length ?? 0
          return (
            <li
              key={transaction.id}
              className="relative flex flex-col gap-2 rounded-lg bg-card px-4 py-3 shadow-card ring-1 ring-foreground/10"
            >
              {/* The whole card opens the entry in full (#359, #739), and it is
                the row's only control: every action (Perbaiki pos, Nota,
                Batalkan) lives in that detail, because controls nested in a
                dense card were easy to mis-tap. A full-card button
                *underneath* the content; the content above ignores pointer
                events, so a tap anywhere on the card falls through to it. The
                receipt count rides in the accessible name, since the glyph
                beside the amount is hidden from screen readers. */}
              <button
                type="button"
                aria-label={
                  copy.transactionDetail.openAria(
                    transferHeadline(transaction) ?? purposeNames.get(transaction.purpose_id) ?? copy.home.purposeUnknown,
                    formatIDR(transaction.amount),
                    formatIsoDate(transaction.occurred_on),
                  ) + (receiptCount > 0 ? `, ${copy.transactionDetail.receiptCountAria(receiptCount)}` : '')
                }
                onClick={() => entry.open(String(transaction.id))}
                className="absolute inset-0 rounded-lg transition-colors outline-none hover:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50"
              />
              <div className="pointer-events-none relative flex items-start justify-between gap-3">
                <span className="flex min-w-0 items-start gap-2">
                  <TransactionGlyph transaction={transaction} />
                  <span className="flex min-w-0 flex-col">
                    {/* The purpose tag is what an entry *was*; a row the app
                      created explains itself on the next line (#257 - a
                      label, built from the row's own facts, never stored
                      text); her own note, when she typed one, comes after
                      that (for a settlement, the settled claim's own note -
                      see noteForDisplay). Date is always last, so the row
                      still answers "what is this?" at a glance. */}
                    {/* A transfer is one row for its two legs (#738), and every
                      kind of it reads the same: neutral, its glyph its
                      label's, its kind word where a pos would lead (the leg's
                      pos is not the movement's), from -> to under it. */}
                    {kind !== null ? (
                      <span className="truncate">{kind}</span>
                    ) : (
                      <PurposeLine transaction={transaction} purposeNames={purposeNames} />
                    )}
                    <TransactionRowLabel transaction={transaction} bare={kind !== null} kindShown={kind !== null} />
                    {note && <span className="truncate text-sm text-muted-foreground">{note}</span>}
                    <span className="text-sm text-muted-foreground">{formatIsoDate(transaction.occurred_on)}</span>
                    {transaction.reversed && <span className="text-sm text-muted-foreground">{copy.dues.history.reversedBadge}</span>}
                  </span>
                </span>
                {/* Amount first and alone on its line, so every row's figure
                  sits flush right whether or not it carries a photo; the
                  camera and count stack underneath as read-only status
                  (#154, #739), shown only once a nota exists. Adding one is
                  the detail's Nota button. */}
                <span className="flex shrink-0 flex-col items-end gap-1.5">
                  <span className="tabular font-medium">{formatIDR(transaction.amount)}</span>
                  {receiptCount > 0 && (
                    <span aria-hidden="true" className="flex items-center gap-1 text-primary">
                      <Camera className="size-4" />
                      <span className="tabular text-sm font-medium">{receiptCount}</span>
                    </span>
                  )}
                </span>
              </div>
              {/* The reversal form (ADR-042), opened from the entry detail and shown under its row. */}
              {onRefresh && reversingId === transaction.id && (
                <div className="pointer-events-none relative [&_button]:pointer-events-auto [&_input]:pointer-events-auto [&_label]:pointer-events-auto">
                  <ReversalForm
                    transaction={transaction}
                    onCancel={() => setReversingId(null)}
                    onReversed={() => {
                      setReversingId(null)
                      toast.success(reverseText.reverseSuccess)
                      void onRefresh()
                    }}
                    refresh={onRefresh}
                  />
                </div>
              )}
            </li>
          )
        })}
        <TransactionDetailDialog
          transaction={detailTransaction}
          purposeNames={purposeNames}
          onClose={entry.close}
          onReverse={
            onRefresh
              ? (transaction) => {
                  handoff.current = { kind: 'reverse', transaction }
                  entry.close()
                  setReversingId(transaction.id)
                }
              : undefined
          }
          onCorrectPurpose={
            onRefresh
              ? (transaction) => {
                  handoff.current = { kind: 'correct', transaction }
                  entry.close()
                }
              : undefined
          }
          onViewReceipts={
            onRefresh
              ? (transaction) => {
                  handoff.current = { kind: 'receipts', transaction }
                  entry.close()
                }
              : undefined
          }
          onReceiptsChanged={onRefresh}
          onCloseAutoFocus={(event) => {
            const next = handoff.current
            if (next === null) return
            handoff.current = null
            event.preventDefault()
            if (next.kind === 'correct') {
              correction.open(`purpose-correction:${next.transaction.id}`)
            } else if (next.kind === 'receipts') {
              setReceiptsForId(next.transaction.id)
            } else {
              // Focus and scroll only once the dialog is gone: a scroll while it
              // is still closing is undone, and leaves the form's buttons under
              // the bottom nav.
              const form = document.getElementById(reversalFormId(next.transaction.id))
              form?.focus({ preventScroll: true })
              form?.scrollIntoView?.({ block: 'nearest' })
            }
          }}
        />
        {onRefresh && (
          <ReceiptDialog
            kind="transactions"
            parentId={receiptsForTransaction?.id ?? null}
            receiptIds={receiptsForTransaction?.receipt_ids ?? []}
            open={receiptsForTransaction !== null}
            onClose={() => setReceiptsForId(null)}
            onChanged={onRefresh}
          />
        )}
        {onRefresh && (
          <CorrectPurposeDialog
            transaction={correcting}
            purposes={selectable ?? []}
            purposeNames={dialogNames}
            open={correcting !== null}
            onClose={correction.close}
            onCorrected={() => {
              correction.close()
              toast.success(copy.purposeCorrection.success)
              void onRefresh()
            }}
            refresh={async () => {
              await onRefresh()
            }}
          />
        )}
      </ul>
    </>
  )
}

/**
 * The row's first line: what this entry was for, as plain text. Correcting it
 * is the detail's Perbaiki pos button (ADR-033, amended by #739), not a tap on
 * this name.
 *
 * The stored pos is what renders, never the effective one, even after a
 * correction has moved the money: the ledger sums stored tags, so showing the
 * effective tag would put the screen out of step with the balances
 * (ADR-033). The PenLine glyph says a correction exists - the same glyph its
 * Perbaikan pos row carries a few rows down, which ties them together - and is hidden from
 * screen readers, with the sentence beside it in sr-only text.
 */
function PurposeLine({ transaction, purposeNames }: { transaction: Transaction; purposeNames: Map<number, string> }) {
  const name = purposeNames.get(transaction.purpose_id) ?? copy.home.purposeUnknown
  const corrected = transaction.effective_purpose_id !== undefined && transaction.effective_purpose_id !== transaction.purpose_id

  return (
    <span className="flex min-w-0 items-center gap-1.5">
      <span className="truncate">{name}</span>
      {corrected && (
        <>
          <PenLine aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
          <span className="sr-only">{copy.purposeCorrection.corrected}</span>
        </>
      )}
    </span>
  )
}

/**
 * The inline form an ordinary transaction is reversed from (ADR-042, ADR-032),
 * opened under the row by the detail dialog's button. Reason (optional) and a
 * yes/no; the date line states when the entry will be dated, and it asks no date.
 * Its own component: the note, busy state and error belong to the one open row.
 */
function reversalFormId(transactionId: number): string {
  return `reversal-form-${transactionId}`
}

function ReversalForm({
  transaction,
  onCancel,
  onReversed,
  refresh,
}: {
  transaction: Transaction
  onCancel: () => void
  onReversed: () => void
  refresh?: () => Promise<void> | void
}) {
  const [note, setNote] = useState('')
  const [state, run] = useApi<Transaction>()

  const busy = state.status === 'loading'

  function handleSubmit() {
    void run(
      async () => {
        const trimmed = note.trim()
        const result = await reverseTransaction(transaction.id, trimmed === '' ? null : trimmed)
        onReversed()
        return result
      },
      { settle: refresh },
    )
  }

  return (
    // scroll-mb matches Shell's main padding, so scrolling it into view brings
    // the buttons above the fixed bottom nav, not under it.
    <div
      id={reversalFormId(transaction.id)}
      tabIndex={-1}
      className="animate-reveal flex scroll-mb-[calc(env(safe-area-inset-bottom)+6rem)] flex-col gap-2 rounded-lg bg-muted/40 p-3 outline-none"
    >
      <div className="flex flex-col gap-1">
        <p className="font-medium">{reverseText.reverse}</p>
        <p className="text-sm text-muted-foreground">{reverseText.reverseExplainer}</p>
        <ReversalDateLine transaction={transaction} />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`reversal-note-${transaction.id}`}>{reverseText.reverseNoteLabel}</Label>
        <Input
          id={`reversal-note-${transaction.id}`}
          type="text"
          className="h-11"
          value={note}
          onChange={(event) => setNote(event.target.value)}
          disabled={busy}
        />
      </div>

      {/* reversalErrorText, not ErrorState.tsx directly (#333): the shared
          fallback chain reads transaction_already_reversed as "iuran",
          which is the wrong noun on this list - see this file's own
          reversalErrorText. */}
      {state.status === 'error' && state.error && (
        <p role="alert" className="text-sm text-attention-text">
          {reversalErrorText(state.error)}
        </p>
      )}

      <div className="grid grid-cols-2 gap-2">
        <Button type="button" variant="outline" size="lg" disabled={busy} onClick={onCancel}>
          {reverseText.reverseCancel}
        </Button>
        <Button type="button" size="lg" disabled={busy} onClick={handleSubmit}>
          {busy ? reverseText.reverseSubmitting : reverseText.reverseConfirm}
        </Button>
      </div>
    </div>
  )
}
