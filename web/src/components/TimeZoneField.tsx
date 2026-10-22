import { copy } from '@/copy/id'
import { DEFAULT_TIME_ZONE, TIME_ZONES, isTimeZone } from '@/lib/timeZones'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

const text = copy.timeZone

/**
 * The fund's time zone (ADR-041): WIB, WITA or WIT, each with an example line.
 * `value` is the IANA name the server stores; one it does not know reads as the
 * default rather than a blank field. The trigger carries `id` for a `<Label htmlFor>`.
 */
export default function TimeZoneField({
  id,
  value,
  onChange,
  disabled = false,
}: {
  id: string
  value: string
  onChange: (zone: string) => void
  disabled?: boolean
}) {
  const shown = isTimeZone(value) ? value : DEFAULT_TIME_ZONE
  return (
    <Select value={shown} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger id={id} aria-label={text.label}>
        <SelectValue>{text.options[shown].label}</SelectValue>
      </SelectTrigger>
      <SelectContent>
        {TIME_ZONES.map((zone) => (
          <SelectItem key={zone} value={zone}>
            <span className="flex flex-col">
              <span>{text.options[zone].label}</span>
              <span className="text-xs text-muted-foreground">{text.options[zone].example}</span>
            </span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
