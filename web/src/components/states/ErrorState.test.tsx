import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('ErrorState', () => {
  it('maps a known error code to its Indonesian copy', () => {
    render(<ErrorState error={new ApiError('not_found', 'The requested resource was not found.')} />)
    expect(screen.getByText(copy.common.errors.not_found)).toBeInTheDocument()
  })

  it('falls back to the warm generic sentence for an unknown code', () => {
    render(<ErrorState error={new ApiError('some_future_code', 'Something the map has never seen.')} />)
    expect(screen.getByText(copy.common.unknownError)).toBeInTheDocument()
  })

  it('never renders the wire message, English by design', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const message = 'The requested resource was not found.'
    render(<ErrorState error={new ApiError('not_found', message)} />)
    expect(screen.queryByText(message)).not.toBeInTheDocument()
  })

  it('logs the wire message to console.error for the maintainer', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const message = 'The requested resource was not found.'
    render(<ErrorState error={new ApiError('not_found', message)} />)
    expect(spy).toHaveBeenCalledWith('API error', 'not_found', message)
  })
})
