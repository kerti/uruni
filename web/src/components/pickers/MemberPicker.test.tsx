import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import MemberPicker, { MemberMultiPicker, OptionalMemberPicker } from '@/components/pickers/MemberPicker'
import { copy } from '@/copy/id'
import { chooseOption, selectOptionNames, selectedOptionName } from '@/test/select'
import type { Member } from '@/lib/setup'

const members: Member[] = [
  {
    id: 1,
    name: 'Jane',
    tier_id: 1,
    joined_on: '2026-01-01',
    inactive_on: null,
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
  },
  {
    id: 2,
    name: 'John',
    tier_id: 2,
    joined_on: '2026-01-01',
    inactive_on: '2026-06-01',
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
  },
  {
    id: 3,
    name: 'Sri',
    tier_id: 1,
    joined_on: '2026-02-01',
    inactive_on: null,
    created_at: 1,
    tier_name: null,
    current_rate: null,
    arrears_months: 0,
  },
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

describe('OptionalMemberPicker (ADR-034, #211)', () => {
  it('shows "Tidak disebutkan" when nothing is chosen, and offers it as an option to clear back to', async () => {
    render(<OptionalMemberPicker id="contributor" label="Dari siapa?" members={members} value={null} onChange={vi.fn()} />)

    expect(selectedOptionName('Dari siapa?')).toBe(copy.record.contributorNone)
    expect(await selectOptionNames('Dari siapa?')).toEqual([copy.record.contributorNone, 'Jane', 'Sri'])
  })

  it('calls onChange with null when "Tidak disebutkan" is chosen after a member', async () => {
    const onChange = vi.fn()
    render(<OptionalMemberPicker id="contributor" label="Dari siapa?" members={members} value={1} onChange={onChange} />)

    await chooseOption('Dari siapa?', copy.record.contributorNone)

    expect(onChange).toHaveBeenCalledWith(null)
  })

  it('calls onChange with the member id when a real member is chosen', async () => {
    const onChange = vi.fn()
    render(<OptionalMemberPicker id="contributor" label="Dari siapa?" members={members} value={null} onChange={onChange} />)

    await chooseOption('Dari siapa?', 'Jane')

    expect(onChange).toHaveBeenCalledWith(1)
  })
})

describe("MemberMultiPicker (ADR-034, #211: an envelope's recipients)", () => {
  it('lists every active member as a checkbox, none checked when value is empty', () => {
    render(
      <MemberMultiPicker label="Untuk siapa?" members={members} value={[]} onChange={vi.fn()} emptyMessage="Belum ada anggota aktif." />,
    )

    expect(screen.getByRole('checkbox', { name: 'Jane' })).not.toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'Sri' })).not.toBeChecked()
    expect(screen.queryByRole('checkbox', { name: 'John' })).not.toBeInTheDocument()
  })

  it('adds a member id on check, and removes it on uncheck', async () => {
    const onChange = vi.fn()
    render(
      <MemberMultiPicker label="Untuk siapa?" members={members} value={[1]} onChange={onChange} emptyMessage="Belum ada anggota aktif." />,
    )

    expect(screen.getByRole('checkbox', { name: 'Jane' })).toBeChecked()

    await userEvent.click(screen.getByRole('checkbox', { name: 'Sri' }))
    expect(onChange).toHaveBeenCalledWith([1, 3])

    await userEvent.click(screen.getByRole('checkbox', { name: 'Jane' }))
    expect(onChange).toHaveBeenCalledWith([])
  })

  it('shows the empty message when the fund has no active member to offer', () => {
    render(<MemberMultiPicker label="Untuk siapa?" members={[]} value={[]} onChange={vi.fn()} emptyMessage="Belum ada anggota aktif." />)

    expect(screen.getByText('Belum ada anggota aktif.')).toBeInTheDocument()
  })
})
