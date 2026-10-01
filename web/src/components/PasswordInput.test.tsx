import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'

import PasswordInput from '@/components/PasswordInput'
import { copy } from '@/copy/id'

describe('PasswordInput', () => {
  it('hides the password until the peek button is pressed, and hides it again', async () => {
    const user = userEvent.setup()
    render(<PasswordInput aria-label="sandi" defaultValue="rahasia" />)

    const field = screen.getByLabelText('sandi')
    const peek = screen.getByRole('button', { name: copy.auth.showPassword })
    expect(field).toHaveAttribute('type', 'password')
    expect(peek).toHaveAttribute('aria-pressed', 'false')

    await user.click(peek)
    expect(field).toHaveAttribute('type', 'text')
    expect(peek).toHaveAttribute('aria-pressed', 'true')

    await user.click(peek)
    expect(field).toHaveAttribute('type', 'password')
  })

  it('disables the peek button with the field', () => {
    render(<PasswordInput aria-label="sandi" disabled />)
    expect(screen.getByRole('button', { name: copy.auth.showPassword })).toBeDisabled()
  })
})
