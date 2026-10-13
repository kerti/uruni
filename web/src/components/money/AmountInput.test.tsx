import { useState } from 'react'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import AmountInput from '@/components/money/AmountInput'

/** Wraps AmountInput as a controlled component the way a real screen would,
 * so a test can type into it like a treasurer would. */
function ControlledAmountInput({ onChange }: { onChange: (amount: number) => void }) {
  const [value, setValue] = useState(0)
  return (
    <AmountInput
      id="amount"
      label="Jumlah"
      value={value}
      onChange={(amount) => {
        setValue(amount)
        onChange(amount)
      }}
    />
  )
}

describe('AmountInput', () => {
  it('accepts digits only while typing and emits a plain integer', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah')
    await userEvent.type(input, 'Rp1a5b0,0.0c0')

    // Every non-digit was dropped on the way in - "150000" survives.
    expect(onChange).toHaveBeenLastCalledWith(150_000)
  })

  it('accepts twelve digits, the ceiling Rp 999.999.999.999', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah') as HTMLInputElement
    await userEvent.type(input, '999999999999')

    expect(input.value).toBe('999999999999')
    expect(onChange).toHaveBeenLastCalledWith(999_999_999_999)
  })

  it('does not accept a thirteenth digit: the field keeps its value, as it does for a non-digit', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah') as HTMLInputElement
    await userEvent.type(input, '9999999999999')

    expect(input.value).toBe('999999999999')
    expect(onChange).toHaveBeenLastCalledWith(999_999_999_999)
    expect(onChange.mock.calls.every(([amount]) => amount <= 999_999_999_999)).toBe(true)
  })

  it('refuses a pasted figure above the ceiling whole, rather than cutting it to twelve digits', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah') as HTMLInputElement
    await userEvent.type(input, '500')
    await userEvent.paste('1.000.000.000.000')

    expect(input.value).toBe('500')
    expect(onChange).toHaveBeenLastCalledWith(500)
  })

  it('formats with Indonesian thousands separators on blur, and reverts to plain digits on focus', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah') as HTMLInputElement
    await userEvent.type(input, '1000000')
    expect(input.value).toBe('1000000')

    await userEvent.tab()
    expect(input.value).toContain('Rp')
    expect(input.value).toContain('1.000.000')

    await userEvent.click(input)
    expect(input.value).toBe('1000000')
  })

  it('leaves a range selected before its caret frame alone, so a select-then-type replaces the pre-filled amount', () => {
    // The e2e flake (dues and reimbursements specs): Playwright's fill()
    // focuses, selects all, then types. When the focus handler's deferred
    // caret move landed between the select and the type, it collapsed the
    // selection and the new digits were appended - 50000 then 23456 posted as
    // 5000023456.
    const frames: FrameRequestCallback[] = []
    const raf = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
      frames.push(cb)
      return frames.length
    })
    try {
      render(<AmountInput id="amount" label="Jumlah" value={50_000} onChange={vi.fn()} />)
      const input = screen.getByLabelText('Jumlah') as HTMLInputElement

      fireEvent.focus(input)
      expect(input.value).toBe('50000')
      input.setSelectionRange(0, input.value.length)
      act(() => frames.forEach((cb) => cb(0)))

      expect([input.selectionStart, input.selectionEnd]).toEqual([0, 5])
    } finally {
      raf.mockRestore()
    }
  })

  it('moves a collapsed caret to the end once focused', () => {
    const frames: FrameRequestCallback[] = []
    const raf = vi.spyOn(window, 'requestAnimationFrame').mockImplementation((cb) => {
      frames.push(cb)
      return frames.length
    })
    try {
      render(<AmountInput id="amount" label="Jumlah" value={50_000} onChange={vi.fn()} />)
      const input = screen.getByLabelText('Jumlah') as HTMLInputElement

      fireEvent.focus(input)
      input.setSelectionRange(2, 2)
      act(() => frames.forEach((cb) => cb(0)))

      expect([input.selectionStart, input.selectionEnd]).toEqual([5, 5])
    } finally {
      raf.mockRestore()
    }
  })

  it('round-trips: typed digits -> formatted display -> the same integer on the next submit', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah')
    await userEvent.type(input, '50000')
    await userEvent.tab()

    expect(onChange).toHaveBeenLastCalledWith(50_000)
  })
})
