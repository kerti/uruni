import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { copy } from '@/copy/id'

/** A tap on the backdrop. Radix listens for pointerdown on the document. */
async function tapOutside() {
  // Radix arms its outside listener on the next tick after mounting.
  await new Promise((resolve) => setTimeout(resolve, 10))
  const overlay = document.querySelector('[data-slot="dialog-overlay"]')
  if (overlay === null) throw new Error('no overlay')
  fireEvent.pointerDown(overlay)
  fireEvent.pointerUp(overlay)
  fireEvent.click(overlay)
}

function renderDialog(onOpenChange = vi.fn(), posts = false) {
  render(
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent closeLabel={copy.common.close} posts={posts}>
        <DialogHeader>
          <DialogTitle>Judul</DialogTitle>
        </DialogHeader>
        <p>Isi</p>
      </DialogContent>
    </Dialog>,
  )
  return onOpenChange
}

describe('Dialog', () => {
  it('closes from the X, a 44px target that is not inside the scrolling body', async () => {
    const onOpenChange = renderDialog()

    const x = screen.getByRole('button', { name: copy.common.close })
    expect(x).toHaveClass('size-11')
    expect(screen.getByRole('dialog')).toContainElement(x)
    expect(x.closest('[data-slot="dialog-body"]')).toBeNull()

    await userEvent.click(x)
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('pins the header on the dialog background while the body scrolls', () => {
    renderDialog()

    const header = screen.getByText('Judul').closest('[data-slot="dialog-header"]')
    expect(header).toHaveClass('sticky', '-top-4', 'border-b')
    expect(header?.closest('[data-slot="dialog-body"]')).toHaveClass('overflow-y-auto')
  })

  it('closes on a tap outside when it posts nothing', async () => {
    const onOpenChange = renderDialog()

    await tapOutside()
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('ignores a tap outside when it posts, and still closes on Escape and the X', async () => {
    const onOpenChange = renderDialog(vi.fn(), true)

    await tapOutside()
    expect(onOpenChange).not.toHaveBeenCalled()

    await userEvent.keyboard('{Escape}')
    expect(onOpenChange).toHaveBeenCalledWith(false)

    onOpenChange.mockClear()
    await userEvent.click(screen.getByRole('button', { name: copy.common.close }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })
})
