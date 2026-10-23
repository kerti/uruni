import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'

import { expect } from './fixtures'

// WCAG 2.1 A and AA, the rules axe can decide on its own. Only serious and
// critical findings fail: moderate and minor ones are axe's best-practice
// advice, not a conformance failure.
const tags = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']
const failing = new Set(['serious', 'critical'])

// expectAccessible scans what the page shows right now, softly: a failing
// screen fails the test but the journey goes on, so one run lists every screen.
// Call it once the screen has settled (after its own expect), so the scan sees
// the rendered state, not a loading one.
//
// No screen needs an exception today (#526). Accepting one means an
// `.exclude(selector)` on the builder below, scoped to the one element, with
// the reason beside it - never a whole rule switched off.
export async function expectAccessible(page: Page) {
  // Screens and dialogs fade in, and a mid-fade label measured 1.4:1 against
  // its own final 7:1. Finite animations only: a spinner never finishes.
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((a) => a.effect?.getComputedTiming().iterations !== Infinity)
        .map((a) => a.finished.catch(() => undefined)),
    ),
  )
  const { violations } = await new AxeBuilder({ page }).withTags(tags).analyze()
  const open = violations
    .filter((v) => v.impact && failing.has(v.impact))
    .flatMap((v) => v.nodes.map((n) => `${v.id} (${v.impact}) ${n.target.join(' ')}: ${n.failureSummary?.split('\n').join(' ')}`))
  expect.soft(open, `accessibility violations on ${page.url()}`).toEqual([])
}
