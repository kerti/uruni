import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'

import { useResetWhen } from '@/lib/useResetWhen'

/** An edit field seeded from `seed` each time `open` turns on - the shape
 * every dialog in #361 has. `tick` forces a re-render with nothing changed. */
function Field({ open, seed, tick }: { open: boolean; seed: string; tick?: number }) {
  const [value, setValue] = useState('')
  useResetWhen(open ? seed : null, () => {
    if (open) setValue(seed)
  })
  return (
    <label>
      field {tick}
      <input value={value} onChange={(event) => setValue(event.target.value)} />
    </label>
  )
}

describe('useResetWhen (#361)', () => {
  it('seeds on the very first render, the moment an effect would have', () => {
    render(<Field open seed="Pelaksan" />)
    expect(screen.getByRole('textbox')).toHaveValue('Pelaksan')
  })

  it('applies the seed before anything can be typed, so a clear and retype is never undone', async () => {
    render(<Field open seed="Pelaksan" />)
    const input = screen.getByRole('textbox')

    await userEvent.clear(input)
    await userEvent.type(input, 'Pelaksana')

    expect(input).toHaveValue('Pelaksana')
  })

  it('leaves her typing alone across re-renders that do not change the key', async () => {
    const { rerender } = render(<Field open seed="Pelaksan" tick={1} />)
    await userEvent.type(screen.getByRole('textbox'), 'a')

    rerender(<Field open seed="Pelaksan" tick={2} />)

    expect(screen.getByRole('textbox')).toHaveValue('Pelaksana')
  })

  it('starts over when the dialog closes and opens again', async () => {
    const { rerender } = render(<Field open seed="Pelaksan" />)
    await userEvent.type(screen.getByRole('textbox'), ' (batal)')

    rerender(<Field open={false} seed="Pelaksan" />)
    rerender(<Field open seed="Pelaksan" />)

    expect(screen.getByRole('textbox')).toHaveValue('Pelaksan')
  })

  it('re-seeds when the thing it was seeded from changes', () => {
    const { rerender } = render(<Field open seed="Pelaksan" />)
    rerender(<Field open seed="Pelaksana" />)
    expect(screen.getByRole('textbox')).toHaveValue('Pelaksana')
  })
})
