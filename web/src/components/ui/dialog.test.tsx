import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { copy } from '@/copy/id'

function renderDialog(onOpenChange = vi.fn()) {
  render(
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent closeLabel={copy.common.close}>
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
})
