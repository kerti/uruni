import { addMonthsToPeriod } from '@/lib/dates'
import type { DuesRate } from '@/lib/setup'

/** The months a rate's correction reprices (ADR-040): from the rate's own
 * month to the month before the next rate that still stands. `until` is null
 * when no later rate stands, i.e. the correction reaches every month onward.
 * Computed from the standing list the API returns (deleted months are already
 * absent), whatever its order. */
export interface RateSpan {
  from: string
  until: string | null
}

export function rateSpan(rates: DuesRate[], rate: DuesRate): RateSpan {
  let next: string | null = null
  for (const other of rates) {
    if (other.effective_from > rate.effective_from && (next === null || other.effective_from < next)) next = other.effective_from
  }
  return { from: rate.effective_from, until: next === null ? null : addMonthsToPeriod(next, -1) }
}
