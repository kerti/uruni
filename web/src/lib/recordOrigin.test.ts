import { describe, expect, it } from 'vitest'

import { recordOriginFrom, returnToOf } from '@/lib/recordOrigin'

describe('recordOrigin', () => {
  it('carries the origin screen with its query', () => {
    expect(recordOriginFrom({ pathname: '/dues', search: '?view=grid&period=2026-08' })).toEqual({
      returnTo: '/dues?view=grid&period=2026-08',
    })
    expect(returnToOf({ returnTo: '/incidentals?purpose=5&from=home' })).toBe('/incidentals?purpose=5&from=home')
  })

  it('returns nothing without an in-app path, so Catat falls back to Beranda', () => {
    expect(returnToOf(null)).toBeNull()
    expect(returnToOf({ dialog: true })).toBeNull()
    expect(returnToOf({ returnTo: 5 })).toBeNull()
    expect(returnToOf({ returnTo: '//evil.example' })).toBeNull()
    expect(returnToOf({ returnTo: 'https://evil.example' })).toBeNull()
  })
})
