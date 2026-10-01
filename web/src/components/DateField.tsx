import { CalendarDays } from 'lucide-react'
import { useState, type CSSProperties } from 'react'
import { Popover } from 'radix-ui'
import { DayPicker } from 'react-day-picker'
import { id as idLocale } from 'react-day-picker/locale'
import 'react-day-picker/style.css'

import { copy } from '@/copy/id'
import { formatIsoDate, parseIsoDate, toIsoDate, type DateBounds } from '@/lib/dates'
import { cn } from '@/lib/utils'

/** The calendar in the design system's own tokens: Forest for the chosen
 * day, 44px day cells for the touch floor. Set on the DayPicker itself -
 * its stylesheet declares these on `.rdp-root`, which beats anything
 * inherited from the popover around it. */
const calendarStyle = {
  '--rdp-accent-color': 'var(--primary)',
  '--rdp-accent-background-color': 'var(--muted)',
  '--rdp-day-height': '44px',
  '--rdp-day-width': '44px',
  '--rdp-day_button-height': '42px',
  '--rdp-day_button-width': '42px',
} as CSSProperties

/** Shared look of DateField's and MonthField's triggers - the same h-11 box
 * as Input, so a date sits in a form like any other field. */
export const pickerTrigger =
  'flex h-11 w-full min-w-0 items-center justify-between gap-2 rounded-lg border border-input bg-transparent px-3 text-left text-base transition-colors outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50'

/** The popover both pickers open into. */
export const pickerPopover = 'z-50 rounded-xl bg-popover p-3 text-popover-foreground shadow-card ring-1 ring-foreground/10'

/**
 * A date field that reads the same on every phone (#197): the trigger shows
 * the date the way the rest of the app writes it ("1 Oktober 2026"), and a
 * tap opens a calendar in a popover. Replaces `<input type="date">`, whose
 * text is drawn by the platform - "3 Sep 2026" on iOS, "09/03/2026" on
 * Android, "yyyy-mm-dd" on desktop Firefox - and which no CSS can reach.
 *
 * `value` and `onChange` speak the wire format, "YYYY-MM-DD", exactly as the
 * native input did, so swapping it in changes nothing about a form's state.
 * `bounds` is the inclusive range on offer (lib/dates' dateBounds): days
 * outside it are disabled, and the header's month and year lists stop at
 * its ends. The trigger carries `id`, so a `<Label htmlFor>` names it.
 */
export default function DateField({
  id,
  value,
  onChange,
  bounds,
  disabled = false,
}: {
  id: string
  value: string
  onChange: (iso: string) => void
  bounds: DateBounds
  disabled?: boolean
}) {
  const [open, setOpen] = useState(false)
  const selected = parseIsoDate(value) ?? undefined
  const min = parseIsoDate(bounds.min) ?? undefined
  const max = parseIsoDate(bounds.max) ?? undefined

  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger id={id} className={pickerTrigger} disabled={disabled}>
        <span className="truncate">{selected ? formatIsoDate(value) : copy.dateField.empty}</span>
        <CalendarDays aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content align="start" sideOffset={6} collisionPadding={16} className={pickerPopover}>
          <DayPicker
            mode="single"
            required
            locale={idLocale}
            captionLayout="dropdown"
            selected={selected}
            defaultMonth={selected ?? max}
            startMonth={min}
            endMonth={max}
            disabled={[...(min ? [{ before: min }] : []), ...(max ? [{ after: max }] : [])]}
            onSelect={(date) => {
              onChange(toIsoDate(date))
              setOpen(false)
            }}
            className={cn('text-base')}
            style={calendarStyle}
          />
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  )
}
