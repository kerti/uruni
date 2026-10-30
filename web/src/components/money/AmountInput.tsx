import { useId, useState, type ChangeEvent, type FocusEvent } from 'react'

import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { exceedsAmountCeiling, formatIDR, parseRupiah } from '@/lib/money'

/**
 * The amount field every record-transaction form needs (PRD section 7.2,
 * Design-System.md's "Amount inputs use inputmode=numeric and format to Rp on
 * blur"). Wraps lib/money.ts: parseRupiah reads only digits (CLAUDE.md rule
 * 1: never a float on the way to the server), formatIDR is the one
 * full-currency rendering.
 *
 * Holds and emits a plain integer (`value`/`onChange`) - the component owns
 * only how that integer is *displayed*. Two display modes, switched on focus:
 *  - focused: the bare digits she is typing, so the caret isn't fighting a
 *    "Rp" prefix or thousands dots;
 *  - blurred: `formatIDR(value)` - "Rp1.000.000" - in tabular figures like
 *    every other amount in the app.
 */
export default function AmountInput({
  id,
  label,
  value,
  onChange,
  disabled,
  labelHidden = false,
  autoFocus = false,
  selectOnFocus = false,
}: {
  id?: string
  label: string
  value: number
  onChange: (amount: number) => void
  disabled?: boolean
  /** The label stays for assistive tech but is not drawn - for a field whose
   * context already says what it is (an inline editor under a row). */
  labelHidden?: boolean
  autoFocus?: boolean
  /** Focus selects the whole figure, so typing replaces it - for an editor
   * opened to change an amount already filled in. */
  selectOnFocus?: boolean
}) {
  const autoId = useId()
  const inputId = id ?? autoId
  const [focused, setFocused] = useState(false)
  const [rawDigits, setRawDigits] = useState('')

  const displayValue = focused ? rawDigits : value === 0 ? '' : formatIDR(value)

  function handleChange(event: ChangeEvent<HTMLInputElement>) {
    const digits = event.target.value.replace(/\D/g, '')
    // A thirteenth digit (typed, or a longer paste) is not accepted: the field
    // keeps what it holds, as it drops a non-digit. 12 digits is the ceiling,
    // Rp999.999.999.999.
    if (exceedsAmountCeiling(digits)) return
    setRawDigits(digits)
    onChange(parseRupiah(digits))
  }

  function handleFocus(event: FocusEvent<HTMLInputElement>) {
    setRawDigits(value === 0 ? '' : String(value))
    setFocused(true)
    // Cursor at the end, not wherever the click landed in the reformatted
    // string - there is no stable caret to preserve across a "Rp1.000.000" ->
    // "1000000" swap. A frame later, so it lands after the tap's caret
    // placement - and only on a collapsed caret: a range selected in that
    // frame (select-all, or Playwright's fill(), which selects then types) is
    // left alone, or the typing would land after the old digits instead of
    // replacing them.
    const input = event.target
    requestAnimationFrame(() => {
      if (selectOnFocus) {
        input.select()
        return
      }
      if (input.selectionStart !== input.selectionEnd) return
      input.setSelectionRange(input.value.length, input.value.length)
    })
  }

  function handleBlur() {
    setFocused(false)
  }

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={inputId} className={labelHidden ? 'sr-only' : undefined}>
        {label}
      </Label>
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
        autoFocus={autoFocus}
      />
    </div>
  )
}
