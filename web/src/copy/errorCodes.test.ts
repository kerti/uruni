import { describe, expect, it } from 'vitest'

import { copy } from '@/copy/id'
import serverCodes from '@/test/wire/error-codes.json'

/** Every `errors` map in the copy, by where it sits: `common.errors` is
 * ErrorState's, and a screen's own map words a code for that screen. */
function errorMaps(node: unknown, path: string[] = []): Map<string, Record<string, unknown>> {
  const maps = new Map<string, Record<string, unknown>>()
  if (!node || typeof node !== 'object') return maps
  for (const [key, value] of Object.entries(node)) {
    if (key === 'errors' && value && typeof value === 'object') maps.set([...path, key].join('.'), value as Record<string, unknown>)
    for (const [where, map] of errorMaps(value, [...path, key])) maps.set(where, map)
  }
  return maps
}

// error-codes.json is every code internal/http can send, kept current by
// TestErrorCodesMatchTheWebList (#524). A code with no copy reaches her as the
// generic "Ada yang tidak beres", so each one must be worded somewhere - in
// common.errors, or in the errors map of the one screen that can meet it.
describe('server error codes', () => {
  it('each has copy', () => {
    const worded = new Set([...errorMaps(copy).values()].flatMap((map) => Object.keys(map)))
    expect(serverCodes.filter((code) => !worded.has(code))).toEqual([])
  })
})
