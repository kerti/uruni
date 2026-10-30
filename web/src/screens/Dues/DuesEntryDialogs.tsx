import { useEffect, useRef, useState } from 'react'

import ReceiptDialog from '@/components/ReceiptDialog'
import ReversalDialog from '@/components/ReversalDialog'
import TransactionDetailDialog from '@/components/TransactionDetailDialog'
import { sharedErrorText } from '@/components/reversalCopy'
import { useToast } from '@/components/toast/useToast'
import { copy } from '@/copy/id'
import { parseDialogTarget } from '@/lib/dialogTarget'
import { listPurposes } from '@/lib/purposes'
import { listTransactions } from '@/lib/transactions'
import { useDialogParam } from '@/lib/useDialogParam'
import type { DuesPaymentHistoryRow } from '@/lib/dues'
import type { Transaction } from '@/lib/transactions'

/**
 * The dialogs behind Riwayat's Iuran cards (#768): the entry detail
 * (`?entry=<id>`, the same one Transaksi opens - a dues payment is an ordinary
 * transaction row), the reversal dialog it hands off to (`?edit=reverse:<id>`,
 * dues wording, ADR-046) and the receipt viewer.
 *
 * The list row (GET /api/dues-payments) is not a Transaction, so the one the
 * detail shows is read through GET /api/transactions filtered by the row's
 * member and period - the same call MemberPayments makes - and picked by id.
 * No new API. Never opens two dialogs at once: the detail's actions close it
 * first and the next one opens from its onCloseAutoFocus.
 */
export default function DuesEntryDialogs({ rows, onRefresh }: { rows: DuesPaymentHistoryRow[]; onRefresh: () => void }) {
  const toast = useToast()
  const entry = useDialogParam('entry')
  const reversal = useDialogParam()
  const reversalTarget = parseDialogTarget('reverse', reversal.value)
  const reversalKind = reversalTarget.kind
  const reversalId = reversalTarget.kind === 'edit' ? reversalTarget.id : null
  const entryId = entry.value !== null && /^\d+$/.test(entry.value) ? Number(entry.value) : null
  const entryMalformed = entry.value !== null && entryId === null

  const [receiptsForId, setReceiptsForId] = useState<number | null>(null)
  const [purposeNames, setPurposeNames] = useState<Map<number, string>>(() => new Map())
  const [version, setVersion] = useState(0)
  const [fetched, setFetched] = useState<{ id: number; transaction: Transaction | null } | null>(null)
  const handoff = useRef<{ kind: 'reverse' | 'receipts'; transaction: Transaction } | null>(null)

  const wantedId = entryId ?? reversalId ?? receiptsForId
  const wantedRow = wantedId === null ? null : (rows.find((r) => r.id === wantedId) ?? null)
  const wantedMember = wantedRow?.member_id
  const wantedPeriod = wantedRow?.dues_period

  useEffect(() => {
    if (wantedId === null || wantedMember === undefined || wantedPeriod === undefined) return
    let live = true
    listTransactions({ memberId: wantedMember, duesPeriod: wantedPeriod })
      .then((page) => {
        if (live) setFetched({ id: wantedId, transaction: page.transactions.find((t) => t.id === wantedId) ?? null })
      })
      .catch(() => {
        if (live) setFetched({ id: wantedId, transaction: null })
      })
    return () => {
      live = false
    }
  }, [wantedId, wantedMember, wantedPeriod, version])

  const wantsPurposes = wantedRow !== null
  useEffect(() => {
    if (!wantsPurposes) return
    let live = true
    listPurposes()
      .then((purposes) => {
        if (live) setPurposeNames(new Map(purposes.map((p) => [p.id, p.name])))
      })
      .catch(() => {})
    return () => {
      live = false
    }
  }, [wantsPurposes])

  // A value naming a row this list does not hold, a malformed one, or a row
  // whose transaction cannot be read: stripped rather than left inert in the
  // URL (clear(), never close() - a dead link is not a navigation to undo).
  const { clear: clearEntry } = entry
  const { clear: clearReversal } = reversal
  const knowsEntry = entryId !== null && rows.some((r) => r.id === entryId)
  const knowsReversal = reversalId !== null && rows.some((r) => r.id === reversalId)
  const unreadable = fetched !== null && fetched.transaction === null
  useEffect(() => {
    if (entryMalformed || (entryId !== null && !knowsEntry) || (entryId !== null && unreadable && fetched?.id === entryId)) clearEntry()
  }, [entryMalformed, entryId, knowsEntry, unreadable, fetched, clearEntry])
  useEffect(() => {
    if (reversalKind === 'foreign') return
    if (reversalKind === 'edit' && (!knowsReversal || (unreadable && fetched?.id === reversalId))) {
      clearReversal()
      return
    }
    if (reversalKind === 'invalid' || reversalKind === 'new') clearReversal()
  }, [reversalKind, reversalId, knowsReversal, unreadable, fetched, clearReversal])

  const transactionFor = (id: number | null): Transaction | null =>
    id !== null && fetched !== null && fetched.id === id ? fetched.transaction : null
  const detailTransaction = transactionFor(entryId)
  const reversing = transactionFor(reversalId)
  const receiptsTransaction = transactionFor(receiptsForId)

  function refresh() {
    setVersion((v) => v + 1)
    onRefresh()
  }

  return (
    <>
      <TransactionDetailDialog
        transaction={detailTransaction}
        purposeNames={purposeNames}
        onClose={entry.close}
        onReverse={(transaction) => {
          handoff.current = { kind: 'reverse', transaction }
          entry.close()
        }}
        onViewReceipts={(transaction) => {
          handoff.current = { kind: 'receipts', transaction }
          entry.close()
        }}
        onReceiptsChanged={refresh}
        onCloseAutoFocus={(event) => {
          const next = handoff.current
          if (next === null) return
          handoff.current = null
          event.preventDefault()
          if (next.kind === 'receipts') setReceiptsForId(next.transaction.id)
          else reversal.open(`reverse:${next.transaction.id}`)
        }}
      />
      <ReceiptDialog
        kind="transactions"
        parentId={receiptsTransaction?.id ?? null}
        receiptIds={receiptsTransaction?.receipt_ids ?? []}
        open={receiptsTransaction !== null}
        onClose={() => setReceiptsForId(null)}
        onChanged={refresh}
      />
      <ReversalDialog
        transaction={reversing}
        open={reversing !== null}
        onClose={reversal.close}
        onReversed={() => {
          reversal.close()
          toast.success(copy.dues.history.success)
          refresh()
        }}
        refresh={refresh}
        // Opened from the entry detail's "Batalkan transaksi", so it reads as
        // it does on Transaksi (title, explainer, buttons); only the error map
        // is the shared one, whose already-reversed wording says "iuran".
        errorText={sharedErrorText}
      />
    </>
  )
}
