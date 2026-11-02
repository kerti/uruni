import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import OpenIncidentalDialog from '@/components/OpenIncidentalDialog'
import { copy } from '@/copy/id'
import { chooseOption } from '@/test/select'
import type { Member } from '@/lib/setup'

const text = copy.incidentals.open

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const budi: Member = {
  id: 7,
  name: 'Budi',
  tier_id: null,
  joined_on: '2026-01-01',
  inactive_on: null,
  created_at: 1,
  tier_name: null,
  current_rate: null,
  arrears_months: 0,
}

/** The roster answers the recipients picker; POST /api/incidentals is
 * recorded so a test can assert what was sent. */
function stub() {
  const posts: unknown[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input.toString()
      if (url.includes('/api/members')) return Promise.resolve(jsonResponse({ members: [budi], next_cursor: null }))
      if (url.includes('/api/incidentals') && init?.method === 'POST') {
        posts.push(JSON.parse(String(init.body)))
        return Promise.resolve(
          jsonResponse(
            {
              purpose_id: 3,
              occasion: 'Kerja bakti',
              target_amount: null,
              opened_on: '2026-09-01',
              closed_on: null,
              created_at: 3,
              minimum_per_member: 25_000,
            },
            201,
          ),
        )
      }
      return Promise.reject(new Error(`unstubbed fetch: ${url}`))
    }),
  )
  return posts
}

// ADR-034: the open dialog also carries the envelope's minimum and its
// recipients - both optional, and posted only what she fills.
describe('OpenIncidentalDialog', () => {
  it('opens an envelope with a minimum and a recipient, both optional fields', async () => {
    const posts = stub()
    const onOpened = vi.fn()
    render(<OpenIncidentalDialog open onClose={vi.fn()} onOpened={onOpened} />)

    const dialog = screen.getByRole('dialog', { name: text.heading })
    await userEvent.type(within(dialog).getByLabelText(text.occasionLabel), 'Kerja bakti')
    await userEvent.type(within(dialog).getByLabelText(text.minimumLabel), '25000')
    await within(dialog).findByRole('combobox', { name: `${text.recipientsLabel} ${text.recipientsAdd}` })
    await chooseOption(`${text.recipientsLabel} ${text.recipientsAdd}`, 'Budi', within(dialog))
    await userEvent.click(within(dialog).getByRole('button', { name: text.submit }))

    await waitFor(() => expect(onOpened).toHaveBeenCalledWith(expect.objectContaining({ purpose_id: 3 })))
    expect(posts[0]).toMatchObject({ occasion: 'Kerja bakti', minimum_per_member: 25_000, recipient_member_ids: [7] })
  })
})
