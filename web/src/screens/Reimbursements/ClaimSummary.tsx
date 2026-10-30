import RecordSummary from '@/components/RecordSummary'
import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import { formatIDR } from '@/lib/money'
import type { Reimbursement } from '@/lib/reimbursements'

const text = copy.reimbursements

/** One Talangan claim as the record a dialog acts on (ADR-046): who fronted
 * it, for which pos, when, the note, and the amount last. Shared by Bayar and
 * Hapus, so both dialogs name the claim the same way. */
export default function ClaimSummary({
  claim,
  memberName,
  purposeName,
}: {
  claim: Reimbursement
  memberName: string
  purposeName: string
}) {
  return (
    <RecordSummary
      items={[
        { label: text.record.memberLabel, value: memberName },
        { label: text.record.purposeLabel, value: purposeName },
        { label: text.record.dateLabel, value: formatIsoDate(claim.incurred_on) },
        ...(claim.note ? [{ label: copy.transactionDetail.noteLabel, value: claim.note, block: true }] : []),
        { label: text.record.amountLabel, value: <span className="tabular font-medium whitespace-nowrap">{formatIDR(claim.amount)}</span> },
      ]}
    />
  )
}
