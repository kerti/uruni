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

  it('accepts twelve digits, the ceiling Rp999.999.999.999', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah') as HTMLInputElement
    await userEvent.type(input, '999999999999')

    expect(input.value).toBe('Rp999.999.999.999')
    expect(onChange).toHaveBeenLastCalledWith(999_999_999_999)
  })

  it('does not accept a thirteenth digit: the field keeps its value, as it does for a non-digit', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah') as HTMLInputElement
    await userEvent.type(input, '9999999999999')

    expect(input.value).toBe('Rp999.999.999.999')
    expect(onChange).toHaveBeenLastCalledWith(999_999_999_999)
    expect(onChange.mock.calls.every(([amount]) => amount <= 999_999_999_999)).toBe(true)
  })

  it('refuses a pasted figure above the ceiling whole, rather than cutting it to twelve digits', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah') as HTMLInputElement
    await userEvent.type(input, '500')
    await userEvent.paste('1.000.000.000.000')

    expect(input.value).toBe('Rp500')
    expect(onChange).toHaveBeenLastCalledWith(500)
  })

  it('groups as she types and reads the same focused or not (#749)', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah') as HTMLInputElement
    await userEvent.type(input, '4500')
    expect(input.value).toBe('Rp4.500')
    await userEvent.type(input, '0')
    expect(input.value).toBe('Rp45.000')
    expect(onChange).toHaveBeenLastCalledWith(45_000)

    await userEvent.tab()
    expect(input.value).toBe('Rp45.000')
    await userEvent.click(input)
    expect(input.value).toBe('Rp45.000')
  })

  it('keeps the caret after the same digit when a dot appears or goes (#749)', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)
    const input = screen.getByLabelText('Jumlah') as HTMLInputElement
    await userEvent.type(input, '45000')

    // A digit typed after the "4" of Rp45.000: Rp415.000, caret after the "1".
    input.setSelectionRange(3, 3)
    await userEvent.type(input, '1', { initialSelectionStart: 3, initialSelectionEnd: 3 })
    expect(input.value).toBe('Rp415.000')
    expect(input.selectionStart).toBe(4)

    // Backspace there takes the "1" back, and the caret stays after the "4".
    await userEvent.type(input, '{Backspace}', { initialSelectionStart: 4, initialSelectionEnd: 4 })
    expect(input.value).toBe('Rp45.000')
    expect(input.selectionStart).toBe(3)
    expect(onChange).toHaveBeenLastCalledWith(45_000)
  })

  it('takes the digit before a dot on backspace, rather than doing nothing (#749)', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)
    const input = screen.getByLabelText('Jumlah') as HTMLInputElement
    await userEvent.type(input, '45000')

    // Caret just after the dot in Rp45.|000.
    await userEvent.type(input, '{Backspace}', { initialSelectionStart: 5, initialSelectionEnd: 5 })
    expect(input.value).toBe('Rp4.000')
    expect(onChange).toHaveBeenLastCalledWith(4_000)
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
      expect(input.value).toBe('Rp50.000')
      input.setSelectionRange(0, input.value.length)
      act(() => frames.forEach((cb) => cb(0)))

      expect([input.selectionStart, input.selectionEnd]).toEqual([0, 8])
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

      expect([input.selectionStart, input.selectionEnd]).toEqual([8, 8])
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
