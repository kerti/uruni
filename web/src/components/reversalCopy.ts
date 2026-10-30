import { copy } from '@/copy/id'
import type { ApiError } from '@/lib/api'

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
}

/** The transaction list's own wording (ADR-042). */
export const transactionReversalCopy: ReversalCopy = {
  title: reverseText.reverse,
  explainer: reverseText.reverseExplainer,
  noteLabel: reverseText.reverseNoteLabel,
  cancel: reverseText.reverseCancel,
  confirm: reverseText.reverseConfirm,
  submitting: reverseText.reverseSubmitting,
}

/** Wire error code -> Indonesian copy, scoped first to the transaction list's
 * wording (transaction_already_reversed reads as "iuran" in the shared map,
 * ADR-042) then falling back to it. */
export function transactionReversalErrorText(err: ApiError): string {
  const specific = reverseText.errors[err.code as keyof typeof reverseText.errors]
  if (specific) return specific
  return sharedErrorText(err)
}

/** The shared map alone: a dues payment's wording is the shared one. */
export function sharedErrorText(err: ApiError): string {
  const common = copy.common.errors[err.code as keyof typeof copy.common.errors]
  return common ?? copy.common.unknownError
}
