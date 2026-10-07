import { afterEach, describe, expect, it, vi } from 'vitest'

import { deleteReceipt, receiptUrl } from '@/lib/receipts'

afterEach(() => {
  vi.unstubAllGlobals()
})

// Ids are module state here, so each test uses its own.
describe('receiptUrl', () => {
  it('is the bare route for an id this page has never deleted', () => {
    expect(receiptUrl(101)).toBe('/api/receipts/101')
  })

  it('changes once this page deletes the id, so a reused id never shows the old photo (#459)', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
    const before = receiptUrl(102)

    await deleteReceipt(102)
    const afterOne = receiptUrl(102)
    expect(afterOne).not.toBe(before)
    expect(afterOne.startsWith('/api/receipts/102')).toBe(true)

    await deleteReceipt(102)
    expect(receiptUrl(102)).not.toBe(afterOne)
    expect(receiptUrl(103)).toBe('/api/receipts/103')
  })

  it('keeps the URL when the delete fails - the photo is still there', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: 'not_found', message: 'x' } }), { status: 404 })),
    )
    await expect(deleteReceipt(104)).rejects.toBeDefined()
    expect(receiptUrl(104)).toBe('/api/receipts/104')
  })
})
