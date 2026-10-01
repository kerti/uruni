import { describe, expect, it } from 'vitest'

import { shownVersion, sourceRef } from '@/lib/build'

describe('sourceRef', () => {
  it('points a tagged build at its tag', () => {
    expect(sourceRef({ version: 'v0.6.0-alpha.9', commit: 'abcdef1234567' })).toBe('v0.6.0-alpha.9')
  })

  it('points an untagged build at its commit', () => {
    expect(sourceRef({ version: 'dev', commit: 'abcdef1234567' })).toBe('abcdef1234567')
  })

  it('falls back to main when nothing names the build', () => {
    expect(sourceRef({ version: 'dev', commit: 'unknown' })).toBe('main')
    expect(sourceRef(null)).toBe('main')
  })
})

describe('shownVersion', () => {
  it('shows a tag alone and a dev build with its short commit', () => {
    expect(shownVersion({ version: 'v0.6.0', commit: 'abcdef1234567' })).toBe('v0.6.0')
    expect(shownVersion({ version: 'dev', commit: 'abcdef1234567' })).toBe('dev (abcdef1)')
    expect(shownVersion({ version: 'dev', commit: 'unknown' })).toBe('dev')
  })
})
