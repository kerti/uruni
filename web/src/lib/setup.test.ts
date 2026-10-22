import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { getFundTimeZone, resetFundTimeZone } from '@/lib/dates'
import { getFund, postSetup, replaceReportSlug, updateFund } from '@/lib/setup'

const fund = {
  id: 1,
  name: 'Kas RT 04',
  currency: 'IDR',
  time_zone: 'Asia/Makassar',
  report_slug: 'kas-rt-04',
  report_url: '/report/kas-rt-04',
  created_at: 1,
}

function stubFetch(body: unknown, status = 200) {
  const fetchMock = vi.fn(() =>
    Promise.resolve(new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })),
  )
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

// ADR-041: every function that returns a fund hands its zone to the date helpers
// before its promise resolves, so the render that follows already reads it.
describe("the fund functions hold the fund's zone (ADR-041)", () => {
  beforeEach(() => {
    resetFundTimeZone()
  })
  afterEach(() => {
    vi.unstubAllGlobals()
    resetFundTimeZone()
  })

  it('getFund sets the zone before it resolves', async () => {
    stubFetch(fund)
    const promise = getFund()
    // Attached before the await: the zone must be set by the time a `.then`
    // registered on the returned promise runs.
    const seen = promise.then(() => getFundTimeZone())
    expect(await seen).toBe('Asia/Makassar')
  })

  it('updateFund sends only what changed, and holds the zone the answer carries', async () => {
    const fetchMock = stubFetch({ ...fund, time_zone: 'Asia/Jayapura' })
    await updateFund({ timeZone: 'Asia/Jayapura' })
    expect(getFundTimeZone()).toBe('Asia/Jayapura')
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/fund')
    expect(init.method).toBe('PATCH')
    expect(JSON.parse(init.body as string)).toEqual({ time_zone: 'Asia/Jayapura' })

    await updateFund({ name: 'Kas Baru' })
    expect(JSON.parse((fetchMock.mock.calls[1] as unknown as [string, RequestInit])[1].body as string)).toEqual({ name: 'Kas Baru' })

    await updateFund({ name: 'Kas Baru', timeZone: 'Asia/Jayapura' })
    expect(JSON.parse((fetchMock.mock.calls[2] as unknown as [string, RequestInit])[1].body as string)).toEqual({
      name: 'Kas Baru',
      time_zone: 'Asia/Jayapura',
    })
  })

  it('replaceReportSlug holds the zone too: it answers with the whole fund', async () => {
    stubFetch({ ...fund, time_zone: 'Asia/Jayapura' })
    await replaceReportSlug()
    expect(getFundTimeZone()).toBe('Asia/Jayapura')
  })

  it('postSetup sends the zone and holds the one the server stored', async () => {
    const fetchMock = stubFetch({ fund: { ...fund, time_zone: 'Asia/Makassar' }, main_purpose_id: 1, accounts: [] }, 201)
    await postSetup('Kas RT 04', 'Asia/Makassar', [{ kind: 'cash', name: 'Tunai' }])
    expect(getFundTimeZone()).toBe('Asia/Makassar')
    const init = (fetchMock.mock.calls[0] as unknown as [string, RequestInit])[1]
    expect(JSON.parse(init.body as string)).toEqual({
      name: 'Kas RT 04',
      time_zone: 'Asia/Makassar',
      accounts: [{ kind: 'cash', name: 'Tunai' }],
    })
  })

  it("does not become the device's zone when a payload carries one it does not know", async () => {
    stubFetch({ ...fund, time_zone: 'Asia/Jayapura' })
    await getFund()
    stubFetch({ ...fund, time_zone: 'America/New_York' })
    await getFund()
    expect(getFundTimeZone()).toBe('Asia/Jayapura')
  })
})
