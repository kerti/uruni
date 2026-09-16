import { act, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import Settings from '@/screens/Settings/index'
import { copy } from '@/copy/id'

// Pengaturan as a whole, rather than one section at a time - which is the
// only way this suite's subject is visible at all.
//
// Two sections own an `?edit=` dialog (Lokasi and Amplop, #263) and every
// section renders against the same URL. Each one strips an `?edit=` value it
// cannot use, so before #263 they stripped each other's: opening Amplop's
// dialog set `?edit=incidental:new`, Lokasi's own cleanup read that as
// malformed the moment its account list resolved, and the param - and with it
// the open dialog - vanished mid-edit. It raced on which list loaded first,
// and e2e caught it as `element was detached from the DOM` in two specs at
// once, including one that had not been touched.
//
// Every section-level suite passed throughout, because a section mounted
// alone has nobody to fight with. Hence this file.

afterEach(() => {
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

/** Every GET the four sections make, all answering non-empty so each list
 * reaches `success` - the state that arms the cleanup effect under test. */
function stubSettings() {
  return vi.fn((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString()
    if (url.includes('/api/incidentals')) {
      return Promise.resolve(
        jsonResponse([
          { purpose_id: 7, occasion: 'Halal bihalal RT', target_amount: null, opened_on: '2026-09-01', closed_on: null, created_at: 7 },
        ]),
      )
    }
    // Not "Tunai": that string is also the cash kind's badge, and these
    // assertions match on text (see the note in the first test), so the
    // fixture's name has to be a string that appears exactly once.
    if (url.includes('/api/accounts')) {
      return Promise.resolve(jsonResponse([{ id: 1, name: 'Dompet Bendahara', kind: 'cash', inactive_on: null }]))
    }
    if (url.includes('/api/purposes')) {
      return Promise.resolve(jsonResponse([{ id: 2, kind: 'pass_through', name: 'Kas Bidang' }]))
    }
    if (url.includes('/api/fund')) {
      return Promise.resolve(jsonResponse({ id: 1, name: 'Kas RT 05' }))
    }
    return Promise.reject(new Error(`unstubbed fetch: ${url}`))
  })
}

function LocationProbe() {
  const location = useLocation()
  return <output data-testid="location-probe">{location.search}</output>
}

function renderAt(entry: string) {
  return render(
    <MemoryRouter initialEntries={[entry]}>
      <Routes>
        <Route
          path="/settings"
          element={
            <>
              <Settings onFundRenamed={vi.fn()} />
              <LocationProbe />
            </>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

function search() {
  return screen.getByTestId('location-probe').textContent
}

/**
 * Gives every pending effect its turn before the param is read back.
 *
 * Without this the survival assertions pass vacuously: the cleanup that used
 * to strip a sibling's param runs in an effect keyed on the list reaching
 * `success`, which lands *after* the render that puts the list's text on
 * screen. Asserting the moment the text appears therefore reads the param
 * before anything has had the chance to remove it, and the test stays green
 * against code that does remove it - which is exactly how it behaved when
 * first written.
 */
async function flushEffects() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

describe('Settings, all sections against one URL', () => {
  it("keeps Amplop's dialog open once every sibling section has loaded", async () => {
    vi.stubGlobal('fetch', stubSettings())
    renderAt('/settings?edit=incidental:new')

    const dialog = await screen.findByRole('dialog', { name: copy.incidentals.open.heading })
    expect(dialog).toBeInTheDocument()

    // Lokasi's list resolving is what used to strip the param: wait for it to
    // be on screen before believing the dialog survived.
    //
    // By text, never by role. Radix inerts everything behind an open modal
    // (dialog.tsx), so *ByRole - which skips aria-hidden subtrees - cannot
    // see a background section at all while a dialog is up, and a wait
    // written that way fails whether or not the bug is present.
    await screen.findByText('Dompet Bendahara')
    await waitFor(() => expect(screen.getByText('Kas Bidang')).toBeInTheDocument())
    await flushEffects()

    // The literal value, not a percent-encoded one: this entry was pushed as
    // a string rather than written through setSearchParams.
    expect(search()).toBe('?edit=incidental:new')
    expect(screen.getByRole('dialog', { name: copy.incidentals.open.heading })).toBeInTheDocument()
  })

  it("keeps Lokasi's dialog open once every sibling section has loaded", async () => {
    vi.stubGlobal('fetch', stubSettings())
    renderAt('/settings?edit=location:new')

    const dialog = await screen.findByRole('dialog', { name: copy.settings.locations.add })
    expect(dialog).toBeInTheDocument()

    // The mirror case: Amplop's own list resolving must not strip a value
    // that belongs to Lokasi. By text, for the inerting reason above.
    await screen.findByText('Halal bihalal RT')
    await flushEffects()

    expect(search()).toBe('?edit=location:new')
    expect(screen.getByRole('dialog', { name: copy.settings.locations.add })).toBeInTheDocument()
  })

  it('still strips a value that is malformed for the section which owns it', async () => {
    vi.stubGlobal('fetch', stubSettings())
    renderAt('/settings?edit=incidental:7')

    // `incidental:` is Amplop's prefix and `7` is not a target it can open
    // (a card navigates to the envelope instead), so this one is its own to
    // clear - the foreign-value rule must not have disabled that.
    await waitFor(() => expect(search()).toBe(''))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})
