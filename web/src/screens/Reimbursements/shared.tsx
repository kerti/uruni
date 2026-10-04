import { copy } from '@/copy/id'
import type { ApiError } from '@/lib/api'

const text = copy.reimbursements

/** Where every Talangan money screen returns to: the tab the claim lives on. */
export const TALANGAN_PATH = '/history/reimbursements'

/**
 * What a Talangan screen hands back to the tab through router location
 * state, the same idiom as App.tsx's HomeState and DuesState: the
 * confirmation belongs to the one history entry that navigation creates, so
 * opening the tab again later does not show a stale one. The tab turns the
 * key into copy; `recordedPhotoFailed` is a claim that saved while its
 * photo did not.
 */
export interface TalanganState {
  reimbursementDone: 'recorded' | 'recordedPhotoFailed' | 'settled' | 'corrected'
}

/** The tab's success line for a finished screen. */
export function doneText(done: TalanganState['reimbursementDone']): string {
  switch (done) {
    case 'recorded':
      return text.record.success
    case 'recordedPhotoFailed':
      return copy.receipts.reimbursementPhotoFailed
    case 'settled':
      return text.settle.success
    case 'corrected':
      return text.correct.success
  }
}

/** Wire error code -> Indonesian copy, scoped first to this feature's own
 * codes (reimbursement_already_settled, reimbursement_waived) then to the
 * shared map; never the English wire message (ADR-014: the API is a code
 * surface). */
export function errorText(err: ApiError): string {
  const specific = text.errors[err.code as keyof typeof text.errors]
  if (specific) return specific
  const common = copy.common.errors[err.code as keyof typeof copy.common.errors]
  return common ?? copy.common.unknownError
}

/** Local YYYY-MM-DD - same helper as RecordTransaction.tsx. */
export function todayISODate(): string {
  const now = new Date()
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  return `${now.getFullYear()}-${mm}-${dd}`
}

/** A failed write's reason, kept above the form so she can read it before
 * deciding again. */
export function FormAlert({ message }: { message: string | null }) {
  if (!message) return null
  return (
    <p role="alert" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention">
      {message}
    </p>
  )
}
