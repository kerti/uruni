import { within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import { copy } from '@/copy/id'
import { openSelect } from '@/test/select'
import type { TimeZone } from '@/lib/timeZones'

/**
 * Opens the fund's time-zone field (components/TimeZoneField) and taps one
 * option. An option's accessible name is its label and its example line
 * together ("WIT - Waktu Indonesia Timurmis. Jayapura, Ambon"), so it is matched
 * by the start of its label.
 */
export async function chooseZoneOption(zone: TimeZone): Promise<void> {
  const listbox = await openSelect(copy.timeZone.label)
  await userEvent.click(within(listbox).getByRole('option', { name: new RegExp(`^${copy.timeZone.options[zone].label}`) }))
}
