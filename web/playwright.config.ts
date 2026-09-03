import { defineConfig } from '@playwright/test'

// E2E (ADR-015's Playwright leg, landed at M6.3). Every value here mirrors a
// constant the Makefile already owns — this file follows those, not the
// other way around, so a bare `npx playwright test` and `make e2e` are never
// two different servers with two different assumptions:
//
//   - E2E_DB   (Makefile) → the URUNI_DB the webServer command below sets.
//   - E2E_PORT (Makefile) → both the webServer's PORT and baseURL below.
//
// `make e2e` itself never reaches this webServer block: it runs `e2e-reset`
// (delete + migrate + seed) *before* `npm run -s test:e2e`, so by the time
// Playwright starts, port 8099 usually has nothing listening on it yet and
// this command boots the real server against the already-seeded database.
// A bare `npx playwright test` run without `make e2e-reset` first still
// boots a server, just against whatever (or nothing) is at that path — the
// config's job is only to reuse the one true command, not to reimplement
// e2e-reset's seeding.
export default defineConfig({
  testDir: './e2e',
  // One database, one worker. Every spec here runs against the single seeded
  // instance the webServer below boots, and each file already opts into
  // serial mode internally for that reason - but files ran in parallel with
  // each other, which held only while no spec changed anything another spec
  // reads. M6.15's settings spec breaks that: it creates a location, and
  // reconcile (golden-path.spec.ts) refuses to submit until every *active*
  // account has been counted, so a location existing for a moment in another
  // file is enough to fail a run that has nothing to do with it. Serialising
  // the whole suite is the honest fix; the per-file `mode: 'serial'` calls
  // stay, since they document each file's own internal ordering.
  fullyParallel: false,
  workers: 1,
  // A stray .only left in a spec must fail CI-shaped runs rather than
  // quietly skip the rest of the suite — moot today with one spec, but the
  // right default from the first file.
  forbidOnly: !!process.env.CI,
  reporter: [['list']],
  use: {
    baseURL: 'http://localhost:8099',
    trace: 'on-first-retry',
  },
  webServer: {
    // The exact command `make e2e-server` wraps (Makefile: E2E_DB, E2E_PORT) —
    // copied, not derived, so the two never drift apart.
    //
    // URUNI_LOG_LEVEL=warn because the request logger (internal/http/
    // middleware.go) writes one info line per request — every asset, every
    // API call — and Playwright pipes this server's stderr into the same
    // terminal as the test results, burying them. warn, not error: a
    // server-side problem during a run is exactly what you need to see, and
    // it still prints.
    command: 'URUNI_DB=/tmp/uruni-e2e.db PORT=8099 URUNI_LOG_LEVEL=warn go run ./cmd/uruni serve',
    cwd: '..',
    url: 'http://localhost:8099/healthz',
    reuseExistingServer: !process.env.CI,
    // Nothing this server writes to stdout is worth interleaving with the
    // reporter; stderr stays piped so a crash or a warn is still visible.
    stdout: 'ignore',
    stderr: 'pipe',
  },
})
