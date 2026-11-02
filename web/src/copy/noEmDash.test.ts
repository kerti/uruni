import { describe, expect, it } from 'vitest'

import source from './id.ts?raw'

// #753: no em dash in anything she reads. The whole file, comments included:
// telling a comment from a string is not worth the parsing. The report's copy
// has its twin in internal/report (TestReportSourcesHaveNoEmDash).
describe('app copy', () => {
  it('has no em dash', () => {
    expect(source).not.toMatch(/\u2014|\\u2014|&mdash;/)
  })
})
