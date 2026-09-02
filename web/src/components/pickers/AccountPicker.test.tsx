import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import AccountPicker from '@/components/pickers/AccountPicker'
import type { Account } from '@/lib/accounts'

const accounts: Account[] = [
  { id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 },
  { id: 2, kind: 'bank', name: 'Bank lama', inactive_on: '2026-01-01', created_at: 1 },
  { id: 3, kind: 'bank', name: 'Bank Uji Coba', inactive_on: null, created_at: 1 },
]

describe('AccountPicker', () => {
  it('excludes a retired account (inactive_on set) from the selectable list', () => {
    render(<AccountPicker id="account" label="Lokasi" accounts={accounts} value={1} onChange={vi.fn()} />)

    expect(screen.getByRole('option', { name: 'Tunai' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'Bank Uji Coba' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'Bank lama' })).not.toBeInTheDocument()
  })

  it('renders only the active accounts even when the selected value is one of them', () => {
    render(<AccountPicker id="account" label="Lokasi" accounts={accounts} value={3} onChange={vi.fn()} />)

    const options = screen.getAllByRole('option')
    expect(options).toHaveLength(2)
  })
})
