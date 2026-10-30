import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import DetailActions from '@/components/DetailActions'

describe('DetailActions (#768)', () => {
  it('lays the actions out in two columns, an odd last one across the row in the destructive tone', () => {
    render(
      <DetailActions
        actions={[
          { key: 'edit', label: 'Ubah', onClick: vi.fn() },
          { key: 'waive', label: 'Putihkan', onClick: vi.fn() },
          { key: 'delete', label: 'Hapus', tone: 'destructive', onClick: vi.fn() },
        ]}
      />,
    )

    const buttons = screen.getAllByRole('button')
    expect(buttons.map((b) => b.textContent)).toEqual(['Ubah', 'Putihkan', 'Hapus'])
    expect(buttons[0]).not.toHaveClass('col-span-2')
    expect(buttons[1]).not.toHaveClass('col-span-2')
    expect(buttons[2]).toHaveClass('col-span-2', 'text-destructive')
  })

  it('pairs an even count with no full-width button', () => {
    render(
      <DetailActions
        actions={[
          { key: 'a', label: 'Nonaktifkan', onClick: vi.fn() },
          { key: 'b', label: 'Hapus', tone: 'destructive', onClick: vi.fn() },
        ]}
      />,
    )

    for (const button of screen.getAllByRole('button')) expect(button).not.toHaveClass('col-span-2')
  })

  it('shows the short label at half width but keeps the full label as the name, and the full label when alone', async () => {
    const onClick = vi.fn()
    const { rerender } = render(
      <DetailActions
        actions={[
          { key: 'fix', label: 'Perbaiki pos', onClick: vi.fn() },
          { key: 'nota', label: 'Tambah foto nota', shortLabel: 'Foto nota', onClick },
        ]}
      />,
    )

    const nota = screen.getByRole('button', { name: 'Tambah foto nota' })
    expect(screen.getByText('Foto nota')).toHaveAttribute('aria-hidden', 'true')
    await userEvent.click(nota)
    expect(onClick).toHaveBeenCalledOnce()

    rerender(<DetailActions actions={[{ key: 'nota', label: 'Tambah foto nota', shortLabel: 'Foto nota', onClick }]} />)
    expect(screen.getByRole('button', { name: 'Tambah foto nota' })).toHaveTextContent(/^Tambah foto nota$/)
  })

  it('renders nothing with no actions', () => {
    const { container } = render(<DetailActions actions={[]} />)
    expect(container).toBeEmptyDOMElement()
  })
})
