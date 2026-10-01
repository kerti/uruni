import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

import { copy } from '@/copy/id'
import { formatPeriod, parseIsoDate } from '@/lib/dates'

/**
 * Drives DateField (#197) the way she does: open it by its label, choose the
 * month and year from the calendar's own header lists, tap the day. The
 * day is found by its accessible name, which react-day-picker writes out in
 * full in the locale ("Kamis, 1 Oktober 2026"), so the match is on the day,
 * month and year rather than a bare "1" that appears in every month.
 */
export async function pickDate(label: string, iso: string): Promise<void> {
  const date = parseIsoDate(iso)
  if (!date) throw new Error(`pickDate: ${iso} is not a YYYY-MM-DD date`)
  await userEvent.click(screen.getByLabelText(label))
  const dialog = await screen.findByRole('dialog')
  const [monthList, yearList] = within(dialog).getAllByRole('combobox')
  await userEvent.selectOptions(yearList, String(date.getFullYear()))
  await userEvent.selectOptions(monthList, String(date.getMonth()))
  const monthName = formatPeriod(iso.slice(0, 7)).split(' ')[0]
  const dayName = new RegExp(`\\b${date.getDate()} ${monthName} ${date.getFullYear()}\\b`)
  await userEvent.click(within(dialog).getByRole('button', { name: dayName }))
}

/** Drives MonthField (#197): open it by its label, step the year arrows to
 * the right year, tap the month. */
export async function pickMonth(label: string, period: string): Promise<void> {
  await userEvent.click(screen.getByLabelText(label))
  const dialog = await screen.findByRole('dialog')
  const target = Number(period.slice(0, 4))
  for (let guard = 0; guard < 200; guard++) {
    const shown = Number(within(dialog).getByText(/^\d{4}$/).textContent)
    if (shown === target) break
    const arrow = shown > target ? copy.dateField.previousYear : copy.dateField.nextYear
    await userEvent.click(within(dialog).getByRole('button', { name: arrow }))
  }
  await userEvent.click(within(dialog).getByRole('button', { name: formatPeriod(period) }))
}
