import { useId, useLayoutEffect, useRef, useState, type ChangeEvent, type FocusEvent } from 'react'

import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { exceedsAmountCeiling, formatIDR, parseRupiah } from '@/lib/money'

/**
 * The amount field every record-transaction form needs (PRD section 7.2).
 * Wraps lib/money.ts: parseRupiah reads only digits (CLAUDE.md rule 1: never
 * a float on the way to the server), formatIDR is the one full-currency
 * rendering.
 *
 * Holds and emits a plain integer (`value`/`onChange`) - the component owns
 * only how that integer is *displayed*, and it is displayed one way, focused
 * or not: "Rp1.000.000", grouped live as she types (#749). Bare digits while
 * focused made a long figure hard to read back before saving. Keeping the
 * caret where she meant it is this component's job: after every edit it
 * lands after the same number of digits it was after, whatever dots were
 * added or dropped around it.
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
  const inputRef = useRef<HTMLInputElement>(null)
  const [focused, setFocused] = useState(false)
  // The digits she has typed, while focused: "0" is a typed zero ("Rp0"),
  // where the empty string is an empty field.
  const [digits, setDigits] = useState('')
  // How many digits sit before the caret after the next render, set by an edit.
  const caretDigits = useRef<number | null>(null)

  const shownDigits = focused ? digits : value === 0 ? '' : String(value)
  const displayValue = shownDigits === '' ? '' : formatIDR(parseRupiah(shownDigits))

  // After an edit re-renders the grouped figure, put the caret back after
  // the same digit it followed.
  useLayoutEffect(() => {
    const input = inputRef.current
    if (caretDigits.current === null || input === null) return
    const position = positionAfterDigits(input.value, caretDigits.current)
    caretDigits.current = null
    input.setSelectionRange(position, position)
  })

  function handleChange(event: ChangeEvent<HTMLInputElement>) {
    const input = event.target
    const caret = input.selectionStart ?? input.value.length
    let next = input.value.replace(/\D/g, '')
    let before = input.value.slice(0, caret).replace(/\D/g, '').length
    // A delete that took only a dot left the digits as they were: it meant
    // the digit beside that dot, so take that one instead.
    if (next === digits && input.value.length < displayValue.length) {
      const inputType = (event.nativeEvent as InputEvent).inputType
      if (inputType === 'deleteContentBackward' && before > 0) {
        next = next.slice(0, before - 1) + next.slice(before)
        before -= 1
      } else if (inputType === 'deleteContentForward' && before < next.length) {
        next = next.slice(0, before) + next.slice(before + 1)
      }
    }
    // "05" reads as Rp5: a leading zero is dropped, and the caret with it.
    const trimmed = next.replace(/^0+(?=\d)/, '')
    before = Math.max(0, before - (next.length - trimmed.length))
    // A thirteenth digit (typed, or a longer paste) is not accepted: the field
    // keeps what it holds, as it drops a non-digit. 12 digits is the ceiling,
    // Rp999.999.999.999.
    if (exceedsAmountCeiling(trimmed)) return
    caretDigits.current = before
    setDigits(trimmed)
    onChange(parseRupiah(trimmed))
  }

  function handleFocus(event: FocusEvent<HTMLInputElement>) {
    setDigits(value === 0 ? '' : String(value))
    setFocused(true)
    // Cursor at the end, not wherever the tap landed - a frame later, so it
    // lands after the tap's own caret placement - and only on a collapsed
    // caret: a range selected in that frame (select-all, or Playwright's
    // fill(), which selects then types) is left alone, or the typing would
    // land after the old digits instead of replacing them.
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
        ref={inputRef}
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

/** The index in `text` just after its `count`-th digit; before the first
 * digit when `count` is 0, so the caret never lands inside "Rp". */
function positionAfterDigits(text: string, count: number): number {
  let seen = 0
  for (let i = 0; i < text.length; i++) {
    if (!/\d/.test(text[i])) continue
    if (seen === count) return i
    seen += 1
  }
  return text.length
}
