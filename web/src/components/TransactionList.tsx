import { Camera, PencilLine } from '@phosphor-icons/react'
import { useEffect, useRef, useState } from 'react'

import ReceiptDialog from '@/components/ReceiptDialog'
import ReversalDialog from '@/components/ReversalDialog'
import TransactionDetailDialog from '@/components/TransactionDetailDialog'
import { transferHeadline, transferKind } from '@/components/rowLabel'
import TransactionGlyph from '@/components/TransactionGlyph'
import TransactionRowLabel from '@/components/TransactionRowLabel'
import { useToast } from '@/components/toast/useToast'
import { copy } from '@/copy/id'
import { formatIsoDate, formatPeriod } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import { noteForDisplay } from '@/lib/transactions'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { listPurposes } from '@/lib/purposes'
import { useDialogParam } from '@/lib/useDialogParam'
import CorrectPurposeDialog from '@/components/CorrectPurposeDialog'
import type { Purpose } from '@/lib/purposes'
import type { Transaction } from '@/lib/transactions'

const reverseText = copy.history.transactions

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
  monthHeaders = false,
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
   * dialog (ADR-033), the reversal dialog (ADR-042, ADR-046), the receipt viewer and the
   * "done" lines, and holds no list state, so the caller only refetches. Omit it
   * and the rows are read-only. */
  onRefresh?: () => Promise<void> | void
  /** Splits the rows under a heading per month (#751), for a list that runs
   * across months: Riwayat with no month filter. The rows arrive newest
   * first, so each month is one run. */
  monthHeaders?: boolean
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
  // The reversal dialog, `?edit=reverse:<id>` (ADR-042, ADR-046): opened by
  // the entry detail's Batalkan once the detail is gone.
  const reversal = useDialogParam()
  const reversalTarget = parseDialogTarget('reverse', reversal.value)
  const reversalKind = reversalTarget.kind
  const reversalId = reversalTarget.kind === 'edit' ? reversalTarget.id : null
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

  // The same strip for `reverse:`: a value naming a row this list does not
  // hold (or malformed) is cleared, a neighbour's value is left alone.
  const { clear: clearReversal } = reversal
  const knowsReversalRow = reversalId !== null && transactions.some((t) => t.id === reversalId)
  useEffect(() => {
    if (reversalKind === 'foreign' || reversalKind === 'new') return
    if (knowsReversalRow) return
    clearReversal()
  }, [reversalKind, knowsReversalRow, clearReversal])

  if (transactions.length === 0) {
    return <p className="text-muted-foreground">{emptyMessage}</p>
  }

  const receiptsForTransaction = transactions.find((t) => t.id === receiptsForId) ?? null
  const correcting = transactions.find((t) => t.id === correctionId) ?? null
  const reversing = transactions.find((t) => t.id === reversalId) ?? null
  const detailTransaction = entry.value === null ? null : (transactions.find((t) => String(t.id) === entry.value) ?? null)

  // The correction dialog names a row's current pos too, which may be one this
  // list's own rows never mention (an envelope's activity knows only itself).
  const dialogNames = new Map([...(selectable ?? []).map((p): [number, string] => [p.id, p.name]), ...purposeNames])

  const groups = monthHeaders ? groupByMonth(transactions) : [{ month: null, rows: transactions }]

  return (
    <>
      {groups.map((group) => (
        <section key={group.month ?? 'all'} className="flex flex-col gap-2">
          {group.month !== null && <h3 className="pt-2 text-sm font-semibold text-muted-foreground">{formatPeriod(group.month)}</h3>}
          <ul className="flex flex-col gap-2">
            {group.rows.map((transaction) => {
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
                </li>
              )
            })}
          </ul>
        </section>
      ))}
      <TransactionDetailDialog
        transaction={detailTransaction}
        purposeNames={purposeNames}
        onClose={entry.close}
        onReverse={
          onRefresh
            ? (transaction) => {
                handoff.current = { kind: 'reverse', transaction }
                entry.close()
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
            reversal.open(`reverse:${next.transaction.id}`)
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
        <ReversalDialog
          transaction={reversing}
          purposeName={reversing ? purposeNames.get(reversing.effective_purpose_id ?? reversing.purpose_id) : undefined}
          open={reversing !== null}
          onClose={reversal.close}
          onReversed={() => {
            reversal.close()
            toast.success(reverseText.reverseSuccess)
            void onRefresh()
          }}
          refresh={onRefresh}
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
 * (ADR-033). The PencilLine glyph says a correction exists - the same glyph its
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
          <PencilLine aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
          <span className="sr-only">{copy.purposeCorrection.corrected}</span>
        </>
      )}
    </span>
  )
}

/** Consecutive rows by their month ("YYYY-MM"), in the order given. */
function groupByMonth(transactions: Transaction[]): { month: string | null; rows: Transaction[] }[] {
  const groups: { month: string | null; rows: Transaction[] }[] = []
  for (const transaction of transactions) {
    const month = transaction.occurred_on.slice(0, 7)
    const last = groups[groups.length - 1]
    if (last && last.month === month) last.rows.push(transaction)
    else groups.push({ month, rows: [transaction] })
  }
  return groups
}
