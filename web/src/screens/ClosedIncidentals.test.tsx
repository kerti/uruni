import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import ClosedIncidentals from '@/screens/ClosedIncidentals'
import { copy } from '@/copy/id'
import type { IncidentalListItem } from '@/lib/incidentals'

afterEach(() => {
  vi.unstubAllGlobals()
})

function envelope(purposeId: number, occasion: string, closedOn: string | null = null): IncidentalListItem {
  return {
    purpose_id: purposeId,
    occasion,
    target_amount: null,
    opened_on: '2026-06-01',
    closed_on: closedOn,
    created_at: purposeId,
    minimum_per_member: null,
    participation: null,
  }
}

function stub(list: IncidentalListItem[]) {
  vi.stubGlobal(
    'fetch',
    vi.fn(() => Promise.resolve(new Response(JSON.stringify(list), { status: 200, headers: { 'Content-Type': 'application/json' } }))),
  )
}

// The screen behind Beranda's closed row (ADR-047) - closed envelopes only,
// most recently closed first, each opening its detail, the way back Beranda.
describe('Closed incidentals', () => {
  it('lists only closed envelopes, most recently closed first, and opens one', async () => {
    stub([envelope(1, 'Halal bihalal RT'), envelope(2, 'Kerja bakti', '2026-07-05'), envelope(3, '17 Agustus', '2026-08-20')])
    const onOpen = vi.fn()
    render(<ClosedIncidentals onBack={vi.fn()} onOpen={onOpen} />)

    const cards = await screen.findAllByRole('button', { name: /^Lihat / })
    expect(cards.map((card) => card.querySelector('.truncate')?.textContent)).toEqual(['17 Agustus', 'Kerja bakti'])
    await userEvent.click(cards[0])
    expect(onOpen).toHaveBeenCalledWith(3)
  })

  it('says so when nothing has been closed yet, and leads back to Beranda', async () => {
    stub([envelope(1, 'Halal bihalal RT')])
    const onBack = vi.fn()
    render(<ClosedIncidentals onBack={onBack} onOpen={vi.fn()} />)

    expect(await screen.findByText(copy.incidentals.closedEmpty)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: copy.incidentals.detail.backToHome }))
    expect(onBack).toHaveBeenCalledTimes(1)
  })
})
