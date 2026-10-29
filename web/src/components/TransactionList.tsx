import { PenLine } from 'lucide-react'
import { useRef, useState } from 'react'

import ReceiptDialog from '@/components/ReceiptDialog'
import ReceiptRowButton from '@/components/ReceiptRowButton'
import ReversalDateLine from '@/components/ReversalDateLine'
import TransactionDetailDialog from '@/components/TransactionDetailDialog'
import { transferHeadline, transferKind } from '@/components/rowLabel'
import TransactionGlyph from '@/components/TransactionGlyph'
import TransactionRowLabel from '@/components/TransactionRowLabel'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { canCorrectPurpose, noteForDisplay, reverseTransaction } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import { useDialogParam } from '@/lib/useDialogParam'
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
  onCorrectPurpose,
  onReceiptsChanged,
  onReversed,
  onReversalRefresh,
}: {
  transactions: Transaction[]
  /** transaction.purpose_id -> its name - a row carries only the id, and
   * GET /api/balances answers with every purpose the fund has (see Home.tsx). */
  purposeNames: Map<number, string>
  emptyMessage: string
  /** Opens the correction dialog for one row (ADR-033). Optional, and that is
   * how Beranda stays inert: Riwayat's Transaksi tab passes it, Home does not,
   * so the same row is a control in the list she works in and plain text in
   * the summary she reads. A row this is passed for may still be ineligible -
   * canCorrectPurpose decides per row, so an ineligible one renders its pos
   * as text rather than a tap that would only be refused. */
  onCorrectPurpose?: (transaction: Transaction) => void
  /** Refetches whatever list this component's `transactions` came from, after
   * a photo is added, replaced or deleted - this component holds no list
   * state. Unlike onCorrectPurpose, both callers pass this: attaching a nota
   * after the fact is offered on Beranda's recent activity too. */
  onReceiptsChanged?: () => void
  /** Reverses an ordinary transaction (ADR-042): optional like onCorrectPurpose,
   * so only Riwayat's Transaksi tab passes it and Beranda stays inert. Gives the
   * entry detail its Batalkan transaksi button; called after the reversal posts so
   * the caller refetches. Whether a row offers it is the server's `reversible`. */
  onReversed?: () => void
  /** Re-reads the caller's list in place when a reversal's response was lost. */
  onReversalRefresh?: () => Promise<void> | void
}) {
  // The row whose photo dialog is open, by id rather than by object, so the
  // dialog reads fresh receipt_ids off the current `transactions` prop the
  // moment the caller's onReceiptsChanged refetch resolves.
  const [receiptsForId, setReceiptsForId] = useState<number | null>(null)
  // The entry read in full, by id in the URL (ADR-032: an open dialog is a
  // search parameter), so back closes it. An id not on this page - a stale
  // deep link - opens nothing.
  const entry = useDialogParam('entry')
  // The row whose reversal form is open under it (ADR-042), if any - one at a
  // time, so the reason always belongs to a single row.
  const [reversingId, setReversingId] = useState<number | null>(null)
  // Set by the entry detail's Batalkan transaksi, so the dialog's close moves
  // focus to the form it opened rather than losing it to the page.
  const handoffId = useRef<number | null>(null)

  if (transactions.length === 0) {
    return <p className="text-muted-foreground">{emptyMessage}</p>
  }

  const receiptsForTransaction = transactions.find((t) => t.id === receiptsForId) ?? null
  const detailTransaction = entry.value === null ? null : (transactions.find((t) => String(t.id) === entry.value) ?? null)

  return (
    <ul className="flex flex-col gap-2">
      {transactions.map((transaction) => {
        const note = noteForDisplay(transaction)
        const kind = transferKind(transaction)
        return (
          <li
            key={transaction.id}
            className="relative flex flex-col gap-2 rounded-lg bg-card px-4 py-3 shadow-card ring-1 ring-foreground/10"
          >
            {/* The whole card opens the entry in full (#359) - as a
                full-card button *underneath* the content, never one wrapped
                around it: the row already holds up to three controls of its
                own, and a button inside a button is invalid and swallows
                their taps. The content above ignores pointer events except
                on its own controls, so a tap on empty card space falls
                through to this button and a tap on a control stays the
                control's. */}
            <button
              type="button"
              aria-label={copy.transactionDetail.openAria(
                transferHeadline(transaction) ?? purposeNames.get(transaction.purpose_id) ?? copy.home.purposeUnknown,
                formatIDR(transaction.amount),
                formatIsoDate(transaction.occurred_on),
              )}
              onClick={() => entry.open(String(transaction.id))}
              className="absolute inset-0 rounded-lg transition-colors outline-none hover:bg-muted/40 focus-visible:ring-3 focus-visible:ring-ring/50"
            />
            <div className="pointer-events-none relative flex items-start justify-between gap-3 [&_button]:pointer-events-auto">
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
                    <PurposeLine transaction={transaction} purposeNames={purposeNames} onCorrectPurpose={onCorrectPurpose} />
                  )}
                  <TransactionRowLabel transaction={transaction} bare={kind !== null} kindShown={kind !== null} />
                  {note && <span className="truncate text-sm text-muted-foreground">{note}</span>}
                  <span className="text-sm text-muted-foreground">{formatIsoDate(transaction.occurred_on)}</span>
                  {transaction.reversed && <span className="text-sm text-muted-foreground">{copy.dues.history.reversedBadge}</span>}
                </span>
              </span>
              {/* Amount first and alone on its line, so every row's figure
                  sits flush right whether or not it carries a photo control;
                  the control stacks underneath, right-aligned. */}
              <span className="flex shrink-0 flex-col items-end gap-1.5">
                <span className="tabular font-medium">{formatIDR(transaction.amount)}</span>
                {/* One control doing double duty (#154): a quiet "Tambah foto
                    nota" with no photo yet, a Forest camera carrying the count
                    once one exists - never a second column of buttons on
                    this list's densest row. Optional, same as
                    onCorrectPurpose above: omitting onReceiptsChanged (no
                    caller does today) drops the control entirely rather than
                    rendering a dead tap. */}
                {onReceiptsChanged && (
                  <ReceiptRowButton receiptIds={transaction.receipt_ids ?? []} onClick={() => setReceiptsForId(transaction.id)} />
                )}
              </span>
            </div>
            {/* The reversal form (ADR-042), opened from the entry detail and shown under its row. */}
            {onReversed && reversingId === transaction.id && (
              <div className="pointer-events-none relative [&_button]:pointer-events-auto [&_input]:pointer-events-auto [&_label]:pointer-events-auto">
                <ReversalForm
                  transaction={transaction}
                  onCancel={() => setReversingId(null)}
                  onReversed={() => {
                    setReversingId(null)
                    onReversed()
                  }}
                  refresh={onReversalRefresh}
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
          onReversed
            ? (transaction) => {
                handoffId.current = transaction.id
                entry.close()
                setReversingId(transaction.id)
              }
            : undefined
        }
        onCloseAutoFocus={(event) => {
          if (handoffId.current === null) return
          // Focus and scroll only once the dialog is gone: a scroll while it
          // is still closing is undone, and leaves the form's buttons under
          // the bottom nav.
          event.preventDefault()
          const form = document.getElementById(reversalFormId(handoffId.current))
          handoffId.current = null
          form?.focus({ preventScroll: true })
          form?.scrollIntoView?.({ block: 'nearest' })
        }}
      />
      {onReceiptsChanged && (
        <ReceiptDialog
          kind="transactions"
          parentId={receiptsForTransaction?.id ?? null}
          receiptIds={receiptsForTransaction?.receipt_ids ?? []}
          open={receiptsForTransaction !== null}
          onClose={() => setReceiptsForId(null)}
          onChanged={onReceiptsChanged}
        />
      )}
    </ul>
  )
}

/**
 * The row's first line: what this entry was for, and - on a row whose pos may
 * still be corrected - the control that corrects it (ADR-033).
 *
 * The pos IS the control, rather than a button beside it. The whole row is
 * not the control because eligibility varies: it would be a dead tap on a
 * dues row and a live one on the expense beneath it, with nothing visible to
 * tell them apart (the row's tap opens the entry detail instead, which every
 * row has). A separate button would break the amount column's right
 * alignment or add 44px to every eligible row on the app's densest list.
 * Tapping the noun that is wrong is also what she means.
 *
 * `py-1.5 -my-1.5` is the 44px target without changing the row's height: the
 * padding grows the hit area and the negative margin gives the space back. A
 * dotted underline is what makes it read as tappable at all.
 *
 * The stored pos is what renders, never the effective one, even after a
 * correction has moved the money: the ledger sums stored tags, so showing the
 * effective tag would put the screen out of step with the balances
 * (ADR-033). The PenLine glyph says a correction exists - the same glyph its
 * Perbaikan pos row carries a few rows down, which ties them together - and is hidden from
 * screen readers, with the sentence beside it in sr-only text.
 */
function PurposeLine({
  transaction,
  purposeNames,
  onCorrectPurpose,
}: {
  transaction: Transaction
  purposeNames: Map<number, string>
  onCorrectPurpose?: (transaction: Transaction) => void
}) {
  const name = purposeNames.get(transaction.purpose_id) ?? copy.home.purposeUnknown
  const corrected = transaction.effective_purpose_id !== undefined && transaction.effective_purpose_id !== transaction.purpose_id

  const marker = corrected ? (
    <>
      <PenLine aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      <span className="sr-only">{copy.purposeCorrection.corrected}</span>
    </>
  ) : null

  if (!onCorrectPurpose || !canCorrectPurpose(transaction)) {
    return (
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="truncate">{name}</span>
        {marker}
      </span>
    )
  }

  return (
    <button
      type="button"
      aria-label={copy.purposeCorrection.controlAria(name)}
      onClick={() => onCorrectPurpose(transaction)}
      className="-my-1.5 flex max-w-full min-w-0 items-center gap-1.5 self-start py-1.5 text-left"
    >
      <span className="truncate underline decoration-dotted underline-offset-4">{name}</span>
      {marker}
    </button>
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
