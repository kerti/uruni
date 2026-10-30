import { copy } from '@/copy/id'
import type { ArrivalToast } from '@/components/toast/useArrivalToast'
import type { ApiError } from '@/lib/api'

const text = copy.reimbursements

/** Where every Talangan money screen returns to: the tab the claim lives on. */
export const TALANGAN_PATH = '/history/reimbursements'

/**
 * What the Talangan correct screen hands back to the tab through router location state,
 * as App.tsx's HomeState and DuesState do: the confirmation belongs to the one
 * history entry that navigation creates, so opening the tab later does not
 * show a stale one. The tab turns the key into copy. (Recording a claim is
 * Catat's, and lands on Beranda.)
 */
export interface TalanganState {
  reimbursementDone: 'corrected'
}

/** useArrivalToast's reader for the tab: the confirmation the correct screen
 * handed back, if any. (Settling is a dialog on the tab, ADR-046, and toasts
 * where it stands.) */
export function pickTalanganArrival(state: unknown): ArrivalToast | null {
  const done = (state as TalanganState | null)?.reimbursementDone
  return done === 'corrected' ? { tone: 'success', text: text.correct.success } : null
}

/** Wire error code -> Indonesian copy, scoped first to this feature's codes
 * (reimbursement_already_settled, reimbursement_waived) then to the shared
 * map; never the English wire message (ADR-014). */
export function errorText(err: ApiError): string {
  const specific = text.errors[err.code as keyof typeof text.errors]
  if (specific) return specific
  const common = copy.common.errors[err.code as keyof typeof copy.common.errors]
  return common ?? copy.common.unknownError
}
