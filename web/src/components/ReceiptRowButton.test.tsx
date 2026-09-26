import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import ReceiptRowButton from '@/components/ReceiptRowButton'
import { copy } from '@/copy/id'

const text = copy.receipts

describe('ReceiptRowButton (#154 follow-up)', () => {
  it('renders the quiet ImagePlus affordance for a row with no photo yet', () => {
    const { container } = render(<ReceiptRowButton receiptIds={[]} onClick={vi.fn()} />)

    expect(screen.getByRole('button', { name: text.addFromRow })).toBeInTheDocument()
    expect(container.querySelector('.lucide-image-plus')).toBeInTheDocument()
    expect(container.querySelector('.lucide-camera')).not.toBeInTheDocument()
  })

  it('renders the Forest Camera icon and the count for a row with photos', () => {
    const { container } = render(<ReceiptRowButton receiptIds={[1, 2]} onClick={vi.fn()} />)

    const button = screen.getByRole('button', { name: text.viewReceipt })
    expect(button).toBeInTheDocument()
    expect(container.querySelector('.lucide-camera')).toBeInTheDocument()
    expect(container.querySelector('.lucide-image-plus')).not.toBeInTheDocument()
    expect(button.textContent).toContain('2')
  })

  it('still shows the count at 1 - a single photo is not treated as "no count"', () => {
    render(<ReceiptRowButton receiptIds={[9]} onClick={vi.fn()} />)

    const button = screen.getByRole('button', { name: text.viewReceipt })
    expect(button.textContent).toContain('1')
  })

  it('calls onClick whichever state it renders', async () => {
    const onClick = vi.fn()
    render(<ReceiptRowButton receiptIds={[1]} onClick={onClick} />)

    await userEvent.click(screen.getByRole('button', { name: text.viewReceipt }))
    expect(onClick).toHaveBeenCalledTimes(1)
  })
})
