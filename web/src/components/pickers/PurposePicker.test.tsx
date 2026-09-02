import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import PurposePicker from '@/components/pickers/PurposePicker'
import type { Purpose } from '@/lib/purposes'

const purposes: Purpose[] = [
  { id: 1, kind: 'main', name: 'Kas utama', created_at: 1 },
  { id: 2, kind: 'pass_through', name: 'Kas Bidang', created_at: 1 },
]

describe('PurposePicker', () => {
  it('lists every purpose the fund has', () => {
    render(<PurposePicker id="purpose" label="Peruntukan" purposes={purposes} value={1} onChange={vi.fn()} />)

    expect(screen.getByRole('option', { name: 'Kas utama' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Kas Bidang' })).toBeInTheDocument()
  })
})
