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
  const addName = `Untuk siapa? ${copy.incidentals.open.recipientsAdd}`

  it('lists only the chosen members, and offers the other active ones in a picker (#768)', async () => {
    render(
      <MemberMultiPicker label="Untuk siapa?" members={members} value={[1]} onChange={vi.fn()} emptyMessage="Belum ada anggota aktif." />,
    )

    expect(screen.getByRole('list')).toHaveTextContent('Jane')
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    // Neither the chosen one nor an inactive one is offered again.
    expect(await selectOptionNames(addName)).toEqual(['Sri'])
  })

  it('adds a member from the picker, and removes one with its X', async () => {
    const onChange = vi.fn()
    render(
      <MemberMultiPicker label="Untuk siapa?" members={members} value={[1]} onChange={onChange} emptyMessage="Belum ada anggota aktif." />,
    )

    await chooseOption(addName, 'Sri')
    expect(onChange).toHaveBeenCalledWith([1, 3])

    await userEvent.click(screen.getByRole('button', { name: copy.incidentals.open.recipientRemoveAria('Jane') }))
    expect(onChange).toHaveBeenCalledWith([])
  })

  it('shows no list and no empty message when nothing is chosen yet, only the picker', () => {
    render(
      <MemberMultiPicker label="Untuk siapa?" members={members} value={[]} onChange={vi.fn()} emptyMessage="Belum ada anggota aktif." />,
    )

    expect(screen.queryByRole('list')).not.toBeInTheDocument()
    expect(screen.queryByText('Belum ada anggota aktif.')).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: addName })).toBeInTheDocument()
  })

  it('shows the empty message when the fund has no active member to offer', () => {
    render(<MemberMultiPicker label="Untuk siapa?" members={[]} value={[]} onChange={vi.fn()} emptyMessage="Belum ada anggota aktif." />)

    expect(screen.getByText('Belum ada anggota aktif.')).toBeInTheDocument()
  })
})
