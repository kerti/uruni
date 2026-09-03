import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import AccountPicker from '@/components/pickers/AccountPicker'
import { selectOptionNames } from '@/test/select'
import type { Account } from '@/lib/accounts'

const accounts: Account[] = [
  { id: 1, kind: 'cash', name: 'Tunai', inactive_on: null, created_at: 1 },
  { id: 2, kind: 'bank', name: 'Bank lama', inactive_on: '2026-01-01', created_at: 1 },
  { id: 3, kind: 'bank', name: 'Bank Uji Coba', inactive_on: null, created_at: 1 },
]

describe('AccountPicker', () => {
  // The options live in a portal that only exists while the popup is open
  // (the themed Select replaced the native one in M6.15), so these assert on
  // what opening it actually offers.
  it('excludes a retired account (inactive_on set) from the selectable list', async () => {
    render(<AccountPicker id="account" label="Lokasi" accounts={accounts} value={1} onChange={vi.fn()} />)

    expect(await selectOptionNames('Lokasi')).toEqual(['Tunai', 'Bank Uji Coba'])
  })

  it('renders only the active accounts even when the selected value is one of them', async () => {
    render(<AccountPicker id="account" label="Lokasi" accounts={accounts} value={3} onChange={vi.fn()} />)

    expect(await selectOptionNames('Lokasi')).toHaveLength(2)
  })
})
