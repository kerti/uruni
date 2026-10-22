import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import DateField from '@/components/DateField'
import MonthField from '@/components/MonthField'
import { Label } from '@/components/ui/label'
import { copy } from '@/copy/id'
import { pickDate, pickMonth } from '@/test/datePicker'
import { resetFundTimeZone, setFundTimeZone } from '@/lib/dates'

function DateHarness({ initial, onChange = vi.fn() }: { initial: string; onChange?: (iso: string) => void }) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <Label htmlFor="d">Tanggal</Label>
      <DateField
        id="d"
        value={value}
        onChange={(next) => {
          setValue(next)
          onChange(next)
        }}
        bounds={{ min: '2025-01-01', max: '2026-10-01' }}
      />
    </>
  )
}

function MonthHarness({ initial, onChange = vi.fn() }: { initial: string; onChange?: (period: string) => void }) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <Label htmlFor="m">Periode</Label>
      <MonthField
        id="m"
        value={value}
        onChange={(next) => {
          setValue(next)
          onChange(next)
        }}
        bounds={{ min: '2025-03', max: '2026-11' }}
      />
    </>
  )
}

describe('DateField (#197)', () => {
  it('shows the date the way the app writes it, on every platform', () => {
    render(<DateHarness initial="2026-09-03" />)
    expect(screen.getByLabelText('Tanggal')).toHaveTextContent('3 September 2026')
  })

  it('picks a day from the calendar and hands back the wire format', async () => {
    const onChange = vi.fn()
    render(<DateHarness initial="2026-10-01" onChange={onChange} />)

    await pickDate('Tanggal', '2025-03-14')

    expect(onChange).toHaveBeenCalledWith('2025-03-14')
    expect(screen.getByLabelText('Tanggal')).toHaveTextContent('14 Maret 2025')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('disables every day past its upper bound, so a future date cannot be tapped', async () => {
    render(<DateHarness initial="2026-10-01" />)
    await userEvent.click(screen.getByLabelText('Tanggal'))
    const dialog = await screen.findByRole('dialog')

    expect(within(dialog).getByRole('button', { name: /\b2 Oktober 2026\b/ })).toBeDisabled()
    expect(within(dialog).getByRole('button', { name: /\b1 Oktober 2026\b/ })).toBeEnabled()
  })

  it('offers only the years inside its bounds', async () => {
    render(<DateHarness initial="2026-10-01" />)
    await userEvent.click(screen.getByLabelText('Tanggal'))
    const [, yearList] = within(await screen.findByRole('dialog')).getAllByRole('combobox')

    expect(
      within(yearList)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['2025', '2026'])
  })
})

describe('MonthField (#197)', () => {
  it('shows the month written out', () => {
    render(<MonthHarness initial="2026-09" />)
    expect(screen.getByLabelText('Periode')).toHaveTextContent('September 2026')
  })

  it('picks a month in another year and hands back YYYY-MM', async () => {
    const onChange = vi.fn()
    render(<MonthHarness initial="2026-09" onChange={onChange} />)

    await pickMonth('Periode', '2025-04')

    expect(onChange).toHaveBeenCalledWith('2025-04')
    expect(screen.getByLabelText('Periode')).toHaveTextContent('April 2025')
  })

  it('disables months outside its bounds and stops the year arrows at them', async () => {
    render(<MonthHarness initial="2026-09" />)
    await userEvent.click(screen.getByLabelText('Periode'))
    const dialog = await screen.findByRole('dialog')

    expect(within(dialog).getByRole('button', { name: 'Desember 2026' })).toBeDisabled()
    expect(within(dialog).getByRole('button', { name: 'November 2026' })).toBeEnabled()
    expect(within(dialog).getByRole('button', { name: copy.dateField.nextYear })).toBeDisabled()

    await userEvent.click(within(dialog).getByRole('button', { name: copy.dateField.previousYear }))
    expect(within(dialog).getByRole('button', { name: 'Februari 2025' })).toBeDisabled()
    expect(within(dialog).getByRole('button', { name: copy.dateField.previousYear })).toBeDisabled()
  })
})

// ADR-041: the calendar's highlighted "today" is the fund's day. DayPicker's own
// default is the device's `new Date()`, so DateField hands it the fund's.
describe("DateField highlights the fund's today (ADR-041)", () => {
  afterEach(() => {
    vi.useRealTimers()
    resetFundTimeZone()
  })

  /** The day cell DayPicker marks as today, in the month the calendar shows. */
  async function todayCell(initial: string): Promise<string | null> {
    render(<DateHarness initial={initial} />)
    await userEvent.click(screen.getByLabelText('Tanggal'))
    const dialog = await screen.findByRole('dialog')
    const cell = dialog.querySelector('[data-today="true"]')
    return cell?.textContent ?? null
  }

  // 22:30 WIB on 30 September is 00:30 WIT on 1 October.
  it.each([
    ['Asia/Jakarta', '2026-09-15', '30'],
    ['Asia/Makassar', '2026-09-15', '30'],
    ['Asia/Jayapura', '2026-10-01', '1'],
  ])('a %s fund at 22:30 WIB shows the %s calendar with day %s as today', async (zone, shown, day) => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-09-30T22:30:00+07:00'))
    setFundTimeZone(zone)
    expect(await todayCell(shown)).toBe(day)
  })
})
