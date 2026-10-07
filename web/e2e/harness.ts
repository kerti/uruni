import { execFile, spawn, type ChildProcess } from 'node:child_process'
import { copyFileSync, mkdirSync, rmSync } from 'node:fs'
import { createServer } from 'node:net'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

// Plain Node, no Playwright: the pieces global-setup.ts (build + templates)
// and fixtures.ts (one server per worker) both need. Everything that reaches
// the Go binary goes through instanceEnv(), so there is exactly one list of
// what an e2e server is told.

export const repoRoot = fileURLToPath(new URL('../../', import.meta.url))

// The one place the e2e root is named: global-setup.ts creates it and exports
// it here, so workers (which are spawned after global setup) inherit it.
export const ROOT_ENV = 'E2E_ROOT'

// seed-e2e refuses any path outside the OS temp dir or without "e2e" in its
// file name (cmd/uruni/seed_e2e.go), so every database file is named this.
export const DB_FILE = 'uruni-e2e.db'

export function e2eRoot(): string {
  const root = process.env[ROOT_ENV]
  if (!root) throw new Error(`${ROOT_ENV} is not set - the e2e global setup did not run (use \`make e2e\` or \`npx playwright test\`)`)
  return root
}

export const binaryPath = () => join(e2eRoot(), 'uruni')
export const templatePath = (seeded: boolean) => join(e2eRoot(), seeded ? 'template-seeded' : 'template-unseeded', DB_FILE)

export type InstancePaths = { db: string; backupDir: string; uploadsDir: string; port: number }

// Every setting the binary reads, stated outright. The Makefile exports .env
// to its children and CI has no .env at all, so nothing here may depend on
// either: URUNI_* from the caller's environment is dropped first, and what
// follows is the whole configuration.
export function instanceEnv(p: InstancePaths): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {}
  for (const [key, value] of Object.entries(process.env)) {
    if (!key.startsWith('URUNI_') && key !== 'PORT') env[key] = value
  }
  return {
    ...env,
    URUNI_DB: p.db,
    URUNI_BACKUP_DIR: p.backupDir,
    URUNI_UPLOADS_DIR: p.uploadsDir,
    URUNI_BASE_URL: `http://localhost:${p.port}`,
    PORT: String(p.port),
    // warn, not error: the request logger writes one info line per request,
    // which buries the reporter, but a server-side problem must still print.
    URUNI_LOG_LEVEL: 'warn',
  }
}

// A port nothing is listening on right now. Bound on all interfaces, like the
// server, so a port taken on only one stack is not offered.
export function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const probe = createServer()
    probe.once('error', reject)
    probe.listen(0, () => {
      const address = probe.address()
      const port = typeof address === 'object' && address ? address.port : 0
      probe.close(() => resolve(port))
    })
  })
}

export function run(command: string, args: string[], env: NodeJS.ProcessEnv, cwd = repoRoot): Promise<void> {
  return new Promise((resolve, reject) => {
    execFile(command, args, { cwd, env }, (error, _stdout, stderr) => {
      if (error) reject(new Error(`${command} ${args.join(' ')} failed: ${error.message}\n${stderr}`))
      else resolve()
    })
  })
}

export function resetFiles(p: InstancePaths, template: string) {
  for (const suffix of ['', '-wal', '-shm', '.lock']) rmSync(p.db + suffix, { force: true })
  for (const dir of [p.backupDir, p.uploadsDir]) {
    rmSync(dir, { recursive: true, force: true })
    // serve refuses to boot when either directory is missing.
    mkdirSync(dir, { recursive: true })
  }
  copyFileSync(template, p.db)
}

export type Server = { child: ChildProcess; stop: () => Promise<void> }

export async function startServer(p: InstancePaths, label: string): Promise<Server> {
  const child = spawn(binaryPath(), ['serve'], { env: instanceEnv(p), stdio: ['ignore', 'ignore', 'pipe'] })
  let stderr = ''
  child.stderr?.on('data', (chunk: Buffer) => {
    stderr += chunk.toString()
    process.stderr.write(`[${label}] ${chunk.toString()}`)
  })
  let exited = false
  child.once('exit', () => (exited = true))

  const stop = async () => {
    if (exited) return
    const gone = new Promise<void>((resolve) => child.once('exit', () => resolve()))
    child.kill('SIGTERM')
    const killer = setTimeout(() => child.kill('SIGKILL'), 10_000)
    await gone
    clearTimeout(killer)
  }

  const url = `http://localhost:${p.port}/healthz`
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    if (exited) throw new Error(`the e2e server exited during boot (code ${child.exitCode}):\n${stderr}`)
    try {
      if ((await fetch(url)).ok) return { child, stop }
    } catch {
      // Not listening yet.
    }
    await new Promise((resolve) => setTimeout(resolve, 50))
  }
  await stop()
  throw new Error(`the e2e server did not answer ${url} within 30s:\n${stderr}`)
}
