import { describe, expect, it } from 'vitest'

import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { errorText, hasCode, nullIfNotFound } from '@/lib/errorCopy'

describe('errorText', () => {
  it("prefers the screen's own words for its code", () => {
    const err = new ApiError('not_found', 'not found')
    expect(errorText(err, { not_found: 'Talangan ini sudah tidak ada.' })).toBe('Talangan ini sudah tidak ada.')
  })

  it('falls back to the shared map', () => {
    const err = new ApiError('not_found', 'not found')
    expect(errorText(err, { other_code: 'x' })).toBe(copy.common.errors.not_found)
    expect(errorText(err)).toBe(copy.common.errors.not_found)
  })

  it('falls back to the generic sentence, never the English wire message', () => {
    const err = new ApiError('something_new', 'An English message')
    expect(errorText(err)).toBe(copy.common.unknownError)
  })
})

describe('hasCode', () => {
  it('matches an ApiError by code only', () => {
    expect(hasCode(new ApiError('not_found', ''), 'not_found')).toBe(true)
    expect(hasCode(new ApiError('invalid_argument', ''), 'not_found')).toBe(false)
    expect(hasCode(new Error('not_found'), 'not_found')).toBe(false)
    expect(hasCode(undefined, 'not_found')).toBe(false)
  })
})

describe('nullIfNotFound', () => {
  it('answers null for not_found and rethrows anything else', () => {
    expect(nullIfNotFound(new ApiError('not_found', ''))).toBeNull()
    const other = new ApiError('network_error', '', true)
    expect(() => nullIfNotFound(other)).toThrow(other)
  })
})
