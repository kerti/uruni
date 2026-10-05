// Riwayat -> Transaksi's filters (#424) as URL state - read by
// History/Transactions.tsx, edited in TransactionFilterDialog.

/** Riwayat -> Transaksi's filters (#424): the public report's four, under
 * the report's own URL params - `month`, `purpose`, `member`, `dir`. */
export interface TransactionFilters {
  month: string | null
  purposeId: number | null
  memberId: number | null
  direction: 'in' | 'out' | null
}

export const noFilters: TransactionFilters = { month: null, purposeId: null, memberId: null, direction: null }

function positiveId(raw: string | null): number | null {
  const n = Number(raw)
  return raw !== null && Number.isInteger(n) && n > 0 ? n : null
}

/** Reads the filters off the URL. A value that cannot be one is treated as
 * absent rather than sent on to be rejected - the same forgiving read the
 * report gives a stale or hand-edited link. */
export function readFilters(params: URLSearchParams): TransactionFilters {
  const month = params.get('month')
  const dir = params.get('dir')
  return {
    month: month !== null && /^\d{4}-(0[1-9]|1[0-2])$/.test(month) ? month : null,
    purposeId: positiveId(params.get('purpose')),
    memberId: positiveId(params.get('member')),
    direction: dir === 'in' || dir === 'out' ? dir : null,
  }
}

/** Writes `filters` onto a copy of `params`, leaving everything else (`q`)
 * alone. */
export function writeFilters(params: URLSearchParams, filters: TransactionFilters): URLSearchParams {
  const next = new URLSearchParams(params)
  const set = (key: string, value: string | number | null) => (value === null ? next.delete(key) : next.set(key, String(value)))
  set('month', filters.month)
  set('purpose', filters.purposeId)
  set('member', filters.memberId)
  set('dir', filters.direction)
  return next
}

export function activeFilterCount(filters: TransactionFilters): number {
  return Object.values(filters).filter((value) => value !== null).length
}
