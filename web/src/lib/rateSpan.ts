import { addMonthsToPeriod } from '@/lib/dates'
import type { DuesRate } from '@/lib/setup'

/** The months a rate's correction reprices (ADR-040): from the rate's own month to
 * the month before the next standing rate. `until` is null when none stands.
 * Computed from the standing list the API returns, whatever its order. */
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

/** The rate that prices `period` (#748): the latest one whose month has
 * started by then. Null when every rate starts later, or there is none. */
export function rateInForce(rates: DuesRate[], period: string): DuesRate | null {
  let inForce: DuesRate | null = null
  for (const rate of rates) {
    if (rate.effective_from <= period && (inForce === null || rate.effective_from > inForce.effective_from)) inForce = rate
  }
  return inForce
}
