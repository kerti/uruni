import { rmSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test as base, type Page } from '@playwright/test'

import { DB_FILE, e2eRoot, freePort, resetFiles, startServer, templatePath, type InstancePaths, type Server } from './harness'

// The treasurer login seed-e2e registers (cmd/uruni/seed_e2e.go) - literals,
// not an import, because that file is Go; only copy crosses the language
// boundary, via the specs' own import of it.
export const seedEmail = 'bendahara@e2e.uruni.test'
export const seedPassword = 'e2e-fixture-password'

export type Instance = {
  // Where this worker's server listens; also what every page's baseURL is.
  url: string
  // Every spec file calls this from beforeAll: it stops the server, puts back
  // a pristine database (seeded unless told otherwise), empties the backup and
  // uploads directories and starts the server again, so a file never sees what
  // another left behind. Tests inside a file stay serial and share the result.
  //
  // `seed: false` is a migrated database with nothing in it - no account, no
  // fund - for the journeys that start at Register and the setup wizard.
  reset: (options?: { seed?: boolean }) => Promise<void>
  running: () => boolean
  paths: InstancePaths
}

// One server per worker, never shared: the one-writer lock (internal/lock)
// forbids two servers on one database file, so the unit that gets isolated is
// the whole process - database, port, backup directory and uploads directory,
// all under a directory named for the worker's index. The in-process login
// limiter and the restore stage slot come along for free.
export const test = base.extend<object, { instance: Instance }>({
  instance: [
    // Playwright reads the first parameter's destructuring to find the fixtures
    // a fixture needs, so even an empty one has to be spelled as a pattern.
    // oxlint-disable-next-line no-empty-pattern
    async ({}, provide, workerInfo) => {
      const dir = join(e2eRoot(), `worker-${workerInfo.parallelIndex}`)
      const paths: InstancePaths = {
        db: join(dir, DB_FILE),
        backupDir: join(dir, 'backups'),
        uploadsDir: join(dir, 'uploads'),
        // Picked once and kept across resets, so a page left open between two
        // files never meets a different server on its port.
        port: await freePort(),
      }
      const label = `e2e w${workerInfo.parallelIndex}`
      let server: Server | undefined

      const instance: Instance = {
        url: `http://localhost:${paths.port}`,
        paths,
        reset: async ({ seed = true } = {}) => {
          await server?.stop()
          server = undefined
          resetFiles(paths, templatePath(seed))
          server = await startServer(paths, label)
        },
        running: () => server !== undefined,
      }
      // Not started here: every file resets first, and booting a server only
      // to throw its database away is the slowest way to begin.
      await provide(instance)

      await server?.stop()
      rmSync(dir, { recursive: true, force: true })
    },
    { scope: 'worker' },
  ],

  // Resolved before beforeAll has run, so it must not look at whether the
  // server is up yet - the port is fixed for the worker's life.
  baseURL: async ({ instance }, provide) => {
    await provide(instance.url)
  },

  // A spec file that forgot its beforeAll would otherwise fail on a refused
  // connection, which says nothing about why.
  page: async ({ page, instance }, provide) => {
    if (!instance.running())
      throw new Error('no e2e server is running: call test.beforeAll(({ instance }) => instance.reset()) in this spec file')
    await provide(page)
  },
})

export { expect }

// Signs in through the API, into the page's own browser context - the same
// session cookie the login form ends up with, minus a screen per test. The one
// journey that is about the form (golden-path.spec.ts) types it for real.
// Call it before the first goto, or reload after; a restore drops every
// session, so a spec that restores calls it again.
export async function logIn(page: Page) {
  const response = await page.context().request.post('/api/login', { data: { email: seedEmail, password: seedPassword } })
  expect(response.ok(), `POST /api/login answered ${response.status()}`).toBe(true)
}
