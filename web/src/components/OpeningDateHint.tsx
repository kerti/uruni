import { copy } from '@/copy/id'
import { formatIsoDate } from '@/lib/dates'
import { isBeforeAnchor, openingAnchor } from '@/lib/openingAnchor'
import type { Account } from '@/lib/accounts'

const text = copy.dateField

/**
 * The non-blocking "this date is before the opening balance" note under a
 * form's date field (#637). Shown for any form that posts a dated entry; it
 * never touches the submit button. Terracotta, never alarm-red
 * (Design-System), role="status" like the other hints: nothing is broken and
 * she may well mean it.
 *
 * `accountIds` are the locations the entry touches: none for a form with no
 * location picker (the fund's earliest opening applies), two for Pindah lokasi.
 */
export default function OpeningDateHint({
  date,
  accounts,
  accountIds,
}: {
  date: string
  accounts: Account[]
  accountIds?: (number | null)[]
}) {
  const anchor = openingAnchor(accounts, accountIds)
  if (anchor === null || !isBeforeAnchor(date, anchor)) return null
  const when = formatIsoDate(anchor.date)
  return (
    <p role="status" className="rounded-lg bg-attention-soft px-3 py-2 text-sm text-attention-text">
      {anchor.locationName === null ? text.beforeOpeningFund(when) : text.beforeOpening(anchor.locationName, when)}
    </p>
  )
}
