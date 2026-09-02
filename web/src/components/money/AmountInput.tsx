import { useId, useState, type ChangeEvent, type FocusEvent } from 'react'

import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { formatIDR, parseRupiah } from '@/lib/money'

/**
 * The amount field every "catat transaksi" form needs (PRD §7.2,
 * Design-System.md's "Amount inputs use inputmode=numeric and format to Rp
 * on blur"). Wraps lib/money.ts rather than re-deriving its parsing -
 * parseRupiah reads only digits (CLAUDE.md rule 1: never a float on the way
 * to the server), formatIDR is the one full-currency rendering.
 *
 * Holds and emits a plain integer (`value`/`onChange`) - the component owns
 * only how that integer is *displayed*, never a string the caller has to
 * re-parse. Two display modes, switched on focus:
 *  - focused: the bare digits she is typing (parseRupiah of the raw input),
 *    so the caret isn't fighting a "Rp" prefix or thousands dots underfoot;
 *  - blurred: `formatIDR(value)` - "Rp 1.000.000" - tabular figures so it
 *    reads like every other amount in the app.
 */
export default function AmountInput({
  id,
  label,
  value,
  onChange,
  disabled,
}: {
  id?: string
  label: string
  value: number
  onChange: (amount: number) => void
  disabled?: boolean
}) {
  const autoId = useId()
  const inputId = id ?? autoId
  const [focused, setFocused] = useState(false)
  const [rawDigits, setRawDigits] = useState('')

  const displayValue = focused ? rawDigits : value === 0 ? '' : formatIDR(value)

  function handleChange(event: ChangeEvent<HTMLInputElement>) {
    const digits = event.target.value.replace(/\D/g, '')
    setRawDigits(digits)
    onChange(parseRupiah(digits))
  }

  function handleFocus(event: FocusEvent<HTMLInputElement>) {
    setRawDigits(value === 0 ? '' : String(value))
    setFocused(true)
    // Cursor at the end, not wherever the click landed in the reformatted
    // string - there is no stable caret position to preserve across a
    // "Rp 1.000.000" -> "1000000" swap.
    requestAnimationFrame(() => event.target.setSelectionRange(event.target.value.length, event.target.value.length))
  }

  function handleBlur() {
    setFocused(false)
  }

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={inputId}>{label}</Label>
      <Input
        id={inputId}
        type="text"
        inputMode="numeric"
        placeholder={formatIDR(0)}
        className="h-11 text-xl font-semibold [font-variant-numeric:tabular-nums]"
        value={displayValue}
        onChange={handleChange}
        onFocus={handleFocus}
        onBlur={handleBlur}
        disabled={disabled}
      />
    </div>
  )
}
