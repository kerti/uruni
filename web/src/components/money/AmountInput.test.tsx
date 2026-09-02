import { useState } from 'react'
import { render, screen } from '@testing-library/react'
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

    // Every non-digit character was dropped on the way in - "150000" is what
    // survives, and that's what onChange was last called with.
    expect(onChange).toHaveBeenLastCalledWith(150_000)
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

  it('round-trips: typed digits -> formatted display -> the same integer on the next submit', async () => {
    const onChange = vi.fn()
    render(<ControlledAmountInput onChange={onChange} />)

    const input = screen.getByLabelText('Jumlah')
    await userEvent.type(input, '50000')
    await userEvent.tab()

    expect(onChange).toHaveBeenLastCalledWith(50_000)
  })
})
