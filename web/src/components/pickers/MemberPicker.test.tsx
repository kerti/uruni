import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import MemberPicker from '@/components/pickers/MemberPicker'
import { selectOptionNames } from '@/test/select'
import type { Member } from '@/lib/setup'

const members: Member[] = [
  { id: 1, name: 'Jane', tier_id: 1, joined_on: '2026-01-01', inactive_on: null, created_at: 1 },
  { id: 2, name: 'John', tier_id: 2, joined_on: '2026-01-01', inactive_on: '2026-06-01', created_at: 1 },
  { id: 3, name: 'Sri', tier_id: 1, joined_on: '2026-02-01', inactive_on: null, created_at: 1 },
]

describe('MemberPicker', () => {
  it('excludes an inactive member (inactive_on set) from the selectable list', async () => {
    render(<MemberPicker id="member" label="Anggota" members={members} value={1} onChange={vi.fn()} />)

    expect(await selectOptionNames('Anggota')).toEqual(['Jane', 'Sri'])
  })

  it('renders only the active members even when the selected value is one of them', async () => {
    render(<MemberPicker id="member" label="Anggota" members={members} value={3} onChange={vi.fn()} />)

    expect(await selectOptionNames('Anggota')).toHaveLength(2)
  })
})
