import { defineConfig } from '@playwright/test'

// E2E (ADR-039; the Playwright leg landed at M6.3). There is no `webServer`
// block and no `baseURL` here: every worker boots its own server on its own
// database, backup directory and uploads directory (e2e/fixtures.ts), and the
// `instance` fixture there supplies the baseURL. The Makefile's E2E_DB /
// E2E_PORT / E2E_BACKUP_DIR describe `make e2e-server`, the single instance
// for debugging by hand; nothing here reads them, and nothing here reads
// `.env` either - CI has none, so every setting is stated per instance.
//
// What a run is: e2e/global-setup.ts builds the binary once and seeds a
// template database; each spec file resets its worker's instance from that
// template in `beforeAll`. The binary embeds web/dist, so `make e2e` builds
// the SPA first; a bare `npx playwright test` tests whatever bundle is there.
export default defineConfig({
  testDir: './e2e',
  globalSetup: './e2e/global-setup.ts',
  // Files run in parallel, one per worker; tests inside a file do not. A file
  // is a story read top to bottom (record, then see it, then undo it), and
  // each opts into serial mode for that reason. fullyParallel stays off so the
  // two never get confused: the isolation unit is the file, not the test.
  fullyParallel: false,
  // Each worker is a browser plus a Go server plus a SQLite file, so this is
  // bounded by what the machine runs comfortably, not by the number of files.
  workers: 2,
  // A stray .only left in a spec must fail CI-shaped runs rather than
  // quietly skip the rest of the suite.
  forbidOnly: !!process.env.CI,
  // In CI (e2e-run.yml) a test gets one retry, so a flake reads as "flaky"
  // in the report rather than a red PR, and the retry records the trace
  // (trace: 'on-first-retry' below) that the failure artifact uploads. The
  // github reporter puts each failure on the PR as an annotation.
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['github']] : [['list']],
  use: {
    // The app is phone-first, and the suite runs the way the treasurer does:
    // 390x844 with touch, for every spec (ruled 2026-10-07; the 800px layout
    // check is its own journey, not a second run of everything). Chromium
    // with a phone's viewport rather than a device descriptor, because those
    // carry an iOS user agent the app should not be tested under.
    viewport: { width: 390, height: 844 },
    isMobile: true,
    hasTouch: true,
    trace: 'on-first-retry',
  },
})
