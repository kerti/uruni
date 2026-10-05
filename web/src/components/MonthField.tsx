import { CalendarDays, ChevronLeft, ChevronRight } from 'lucide-react'
import { useState } from 'react'
import { Popover } from 'radix-ui'

import { pickerPopover, pickerTrigger } from '@/components/DateField'
import { copy } from '@/copy/id'
import { formatPeriod, type DateBounds } from '@/lib/dates'
import { cn } from '@/lib/utils'

const text = copy.dateField

// "Jan", "Feb", ... "Agu", "Des" - id-ID's own short month names, so the
// grid needs no copy strings of its own.
const shortMonth = new Intl.DateTimeFormat('id-ID', { month: 'short' })
const MONTH_LABELS = Array.from({ length: 12 }, (_, i) => shortMonth.format(new Date(2000, i, 1)))

/**
 * A month field for the two places that take a month, not a day (#197): a
 * rate's start month and the dues status period. A day grid would ask for a
 * day that means nothing there, so this opens a year with a 4x3 grid of
 * months instead - the shape Balances' MonthPickerPopover settled on, in the
 * same trigger and popover as DateField so the two read as one family.
 *
 * `value` and `onChange` speak "YYYY-MM", the wire format, exactly as
 * `<input type="month">` did. `bounds` ("YYYY-MM" both ends, inclusive)
 * disables the months outside it and stops the year arrows at its years.
 */
export default function MonthField({
  id,
  value,
  onChange,
  bounds,
  emptyLabel = text.emptyMonth,
  disabled = false,
}: {
  id: string
  value: string
  onChange: (period: string) => void
  bounds: DateBounds
  /** What an empty field says - Riwayat's filter (#424) reads "Semua". */
  emptyLabel?: string
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const valueYear = Number(value.slice(0, 4)) || Number(bounds.max.slice(0, 4))
  const [year, setYear] = useState(valueYear)
  const minYear = Number(bounds.min.slice(0, 4))
  const maxYear = Number(bounds.max.slice(0, 4))

  return (
    <Popover.Root
      open={open}
      onOpenChange={(next) => {
        // Every opening starts on the chosen month's year, not wherever the
        // arrows were left last time.
        if (next) setYear(valueYear)
        setOpen(next)
      }}
    >
      <Popover.Trigger id={id} className={pickerTrigger} disabled={disabled}>
        <span className="truncate">{value ? formatPeriod(value) : emptyLabel}</span>
        <CalendarDays aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content align="start" sideOffset={6} collisionPadding={16} className={cn(pickerPopover, 'w-72')}>
          <div className="mb-2 flex items-center justify-between">
            <button
              type="button"
              aria-label={text.previousYear}
              className="flex size-11 items-center justify-center rounded-lg text-primary outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-30"
              onClick={() => setYear((y) => y - 1)}
              disabled={year <= minYear}
            >
              <ChevronLeft aria-hidden="true" className="size-5" />
            </button>
            <span className="tabular font-semibold" aria-live="polite">
              {year}
            </span>
            <button
              type="button"
              aria-label={text.nextYear}
              className="flex size-11 items-center justify-center rounded-lg text-primary outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-30"
              onClick={() => setYear((y) => y + 1)}
              disabled={year >= maxYear}
            >
              <ChevronRight aria-hidden="true" className="size-5" />
            </button>
          </div>
          <div className="grid grid-cols-4 gap-1">
            {MONTH_LABELS.map((label, i) => {
              const period = `${year}-${String(i + 1).padStart(2, '0')}`
              const selected = period === value
              return (
                <button
                  key={period}
                  type="button"
                  aria-label={formatPeriod(period)}
                  aria-pressed={selected}
                  disabled={period < bounds.min || period > bounds.max}
                  onClick={() => {
                    onChange(period)
                    setOpen(false)
                  }}
                  className={cn(
                    'flex h-11 items-center justify-center rounded-lg text-base outline-none focus-visible:ring-3 focus-visible:ring-ring/50 disabled:opacity-30',
                    selected ? 'bg-primary font-medium text-primary-foreground' : 'hover:bg-muted',
                  )}
                >
                  {label}
                </button>
              )
            })}
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}
