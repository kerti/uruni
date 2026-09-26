import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import ReceiptViewer from '@/components/ReceiptViewer'
import { copy } from '@/copy/id'

describe('ReceiptViewer (#154 follow-up)', () => {
  it('opens at actual size, and a tap on the photo goes back', async () => {
    const onClose = vi.fn()
    render(<ReceiptViewer open src="/api/receipts/9" anchor={{ fx: 0.5, fy: 0.5 }} onClose={onClose} />)

    // The photo is decorative (alt="", the same split every other image in
    // this app uses) and only ever one at a time, so it is queried by tag
    // rather than role. Radix portals the dialog onto `document.body`,
    // outside the render container, so it is queried from there.
    const image = document.querySelector('img') as HTMLImageElement
    expect(image).toHaveAttribute('src', '/api/receipts/9')
    // Natural size: nothing scales it down to the screen.
    expect(image).toHaveClass('max-w-none')
    expect(image).not.toHaveClass('object-contain')

    await userEvent.click(image)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('closes on the close button', async () => {
    const onClose = vi.fn()
    render(<ReceiptViewer open src="/api/receipts/9" anchor={null} onClose={onClose} />)

    await userEvent.click(screen.getByRole('button', { name: copy.common.close }))
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('closes on Escape', async () => {
    const onClose = vi.fn()
    render(<ReceiptViewer open src="/api/receipts/9" anchor={null} onClose={onClose} />)

    await userEvent.keyboard('{Escape}')
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('renders nothing to view when closed', () => {
    render(<ReceiptViewer open={false} src={null} anchor={null} onClose={vi.fn()} />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})
