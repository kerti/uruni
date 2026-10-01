import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { useDialogParam, useRefetchKey } from '@/lib/useDialogParam'

/** Renders the hook plus enough chrome to drive and observe it: the current
 * search string, an open/close pair, and a manual "go back once more"
 * button - the only way from outside to tell a pushed entry from a replaced
 * one is to see what a further back-navigation lands on. */
function Probe({ onBackAgain }: { onBackAgain?: (pathname: string) => void }) {
  const { value, open, close } = useDialogParam()
  const location = useLocation()
  const navigate = useNavigate()

  return (
    <div>
      <output data-testid="search">{location.search}</output>
      <output data-testid="value">{value ?? ''}</output>
      <button onClick={() => open('foo')}>open</button>
      <button onClick={close}>close</button>
      <button
        onClick={() => {
          navigate(-1)
          onBackAgain?.(window.location.pathname)
        }}
      >
        back-again
      </button>
    </div>
  )
}

function BackLanding() {
  const location = useLocation()
  return <output data-testid="landed-on">{location.pathname}</output>
}

describe('useDialogParam', () => {
  it('open() pushes ?edit=<value>; close() navigates back to where it was opened from', async () => {
    render(
      <MemoryRouter initialEntries={['/list']}>
        <Routes>
          <Route path="/list" element={<Probe />} />
        </Routes>
      </MemoryRouter>,
    )

    expect(screen.getByTestId('search')).toHaveTextContent('')

    await userEvent.click(screen.getByRole('button', { name: 'open' }))
    expect(screen.getByTestId('search')).toHaveTextContent('?edit=foo')
    expect(screen.getByTestId('value')).toHaveTextContent('foo')

    await userEvent.click(screen.getByRole('button', { name: 'close' }))
    expect(screen.getByTestId('search')).toHaveTextContent('')
  })

  it('a dialog opened from within the app leaves exactly one entry to undo - closing it lands one step behind where it opened, not two', async () => {
    render(
      <MemoryRouter initialEntries={['/start', '/list']} initialIndex={1}>
        <Routes>
          <Route path="/start" element={<BackLanding />} />
          <Route path="/list" element={<Probe />} />
        </Routes>
      </MemoryRouter>,
    )

    await userEvent.click(screen.getByRole('button', { name: 'open' }))
    expect(screen.getByTestId('search')).toHaveTextContent('?edit=foo')

    await userEvent.click(screen.getByRole('button', { name: 'close' }))
    // Back to /list with no dialog - the push open() made was undone, not
    // stacked on top of.
    expect(screen.getByTestId('search')).toHaveTextContent('')

    // One more back-navigation should land on /start, proving close()
    // consumed exactly the one entry open() pushed - not zero (a leftover
    // dialog entry) and not two (it ate into history that came before it).
    await userEvent.click(screen.getByRole('button', { name: 'back-again' }))
    expect(await screen.findByTestId('landed-on')).toHaveTextContent('/start')
  })

  it('close() on a deep link (no entry pushed by open) strips the param with replace, never navigating out of the app', async () => {
    render(
      <MemoryRouter initialEntries={['/start', '/list?edit=foo']} initialIndex={1}>
        <Routes>
          <Route path="/start" element={<BackLanding />} />
          <Route path="/list" element={<Probe />} />
        </Routes>
      </MemoryRouter>,
    )

    expect(screen.getByTestId('search')).toHaveTextContent('?edit=foo')

    await userEvent.click(screen.getByRole('button', { name: 'close' }))
    await waitFor(() => expect(screen.getByTestId('search')).toHaveTextContent(''))
    // Still on /list - close() never navigated away from the screen for a
    // link that landed straight on the dialog.
    expect(screen.queryByTestId('landed-on')).not.toBeInTheDocument()

    // The strip was a replace, not a push: a single back-navigation from
    // here goes straight to /start, not back to the (now-gone) ?edit=foo.
    await userEvent.click(screen.getByRole('button', { name: 'back-again' }))
    expect(await screen.findByTestId('landed-on')).toHaveTextContent('/start')
  })
})

/** The refetch signal plus a dialog and an ordinary same-route navigation,
 * so the test can see which of them move it (#359). */
function RefetchProbe() {
  const refetchKey = useRefetchKey()
  const { open, close, clear } = useDialogParam('entry')
  const navigate = useNavigate()
  return (
    <div>
      <output data-testid="refetch-key">{refetchKey}</output>
      <button onClick={() => open('7')}>open</button>
      <button onClick={close}>close</button>
      <button onClick={clear}>clear</button>
      <button onClick={() => navigate('/list')}>renavigate</button>
    </div>
  )
}

function renderRefetchProbe(initial = '/list') {
  render(
    <MemoryRouter initialEntries={[initial]}>
      <Routes>
        <Route path="/list" element={<RefetchProbe />} />
      </Routes>
    </MemoryRouter>,
  )
  return () => screen.getByTestId('refetch-key').textContent
}

describe('useRefetchKey (#359)', () => {
  it('stays put while a dialog opens and closes, so the list under it is not reloaded', async () => {
    const key = renderRefetchProbe()
    const before = key()

    await userEvent.click(screen.getByRole('button', { name: 'open' }))
    expect(key()).toBe(before)

    await userEvent.click(screen.getByRole('button', { name: 'close' }))
    expect(key()).toBe(before)
  })

  it('stays put when a deep-linked dialog is cleared in place', async () => {
    const key = renderRefetchProbe('/list?entry=7')
    const before = key()

    await userEvent.click(screen.getByRole('button', { name: 'clear' }))
    expect(key()).toBe(before)
  })

  it('still moves on an ordinary navigation, which is what reloads a screen after a write elsewhere', async () => {
    const key = renderRefetchProbe()
    const before = key()

    await userEvent.click(screen.getByRole('button', { name: 'renavigate' }))
    expect(key()).not.toBe(before)
  })
})
