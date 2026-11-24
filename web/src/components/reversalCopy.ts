import { copy } from '@/copy/id'
import type { ErrorMessages } from '@/lib/errorCopy'

const reverseText = copy.history.transactions

/** The words one reversal dialog carries. Riwayat's transaction list and a
 * member's dues payments say the same thing in their own nouns, so each hands
 * in its own; `explainer` is only where there is one to say. */
export interface ReversalCopy {
  title: string
  explainer?: string
  noteLabel: string
  cancel: string
  confirm: string
  submitting: string
  /** Words for refusals checked before the shared map (lib/errorCopy.ts). */
  errors?: ErrorMessages
}

/** The transaction list's own wording (ADR-042). */
export const transactionReversalCopy: ReversalCopy = {
  title: reverseText.reverse,
  explainer: reverseText.reverseExplainer,
  noteLabel: reverseText.reverseNoteLabel,
  cancel: reverseText.reverseCancel,
  confirm: reverseText.reverseConfirm,
  submitting: reverseText.reverseSubmitting,
  // transaction_already_reversed reads as "iuran" in the shared map (ADR-042).
  errors: reverseText.errors,
}

/** A dues payment's reversal: opened from the entry detail's "Batalkan
 * transaksi", so it reads as it does on Transaksi (title, explainer,
 * buttons); only the refusals are the shared map's, whose already-reversed
 * wording says "iuran". */
export const duesReversalCopy: ReversalCopy = { ...transactionReversalCopy, errors: undefined }
