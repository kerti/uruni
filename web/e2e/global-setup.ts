import { mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'

import { ROOT_ENV, binaryPath, instanceEnv, run, templatePath } from './harness'

// Once per run, before any worker: build the binary and make the two database
// templates every instance is reset from. Building here (not `go run` per
// server) keeps a reset to a process start, and seeding here (not per reset)
// keeps argon2id's password hashing out of every spec file.
//
// The binary embeds web/dist, so a stale bundle is tested if it was not built
// first; `make e2e` builds it, a bare `npx playwright test` takes what is there.
export default async function globalSetup() {
  // Under the OS temp dir with "e2e" in the name: seed-e2e's path guard.
  const root = mkdtempSync(join(tmpdir(), 'uruni-e2e-'))
  process.env[ROOT_ENV] = root

  await run('go', ['build', '-o', binaryPath(), './cmd/uruni'], process.env)

  // seed-e2e and migrate never listen or write a dump, but migrate up records
  // the booted version (and any pre-upgrade snapshot) in the backup directory,
  // so that one has to exist.
  const scratch = { backupDir: join(root, 'template-backups'), uploadsDir: join(root, 'unused-uploads'), port: 8099 }
  mkdirSync(scratch.backupDir, { recursive: true })
  for (const seeded of [true, false]) {
    const db = templatePath(seeded)
    mkdirSync(dirname(db), { recursive: true })
    const env = instanceEnv({ ...scratch, db })
    // Migrated only, for the register and setup journeys; serve would migrate
    // on boot anyway, doing it here keeps that out of the reset.
    await run(binaryPath(), seeded ? ['seed-e2e'] : ['migrate', 'up'], env)
  }

  return async () => {
    rmSync(root, { recursive: true, force: true })
  }
}
