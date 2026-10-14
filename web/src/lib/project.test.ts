import { afterEach, describe, expect, it, vi } from 'vitest'

// repoURL is read from the build environment when the module loads, so each
// case stubs the variable and imports a fresh copy.
async function load() {
  vi.resetModules()
  return import('@/lib/project')
}

describe('repoURL', () => {
  afterEach(() => {
    vi.unstubAllEnvs()
  })

  it('is upstream when no source URL was set at build time', async () => {
    vi.stubEnv('VITE_SOURCE_URL', '')
    const { repoURL, sourceURL } = await load()
    expect(repoURL).toBe('https://github.com/kerti/uruni')
    expect(sourceURL(null)).toMatch(/^https:\/\/github\.com\/kerti\/uruni\/tree\//)
  })

  it("is a fork's own repository when the build sets one, without a trailing slash", async () => {
    vi.stubEnv('VITE_SOURCE_URL', 'https://git.example.org/fork/uruni/')
    const { repoURL, licenseURL } = await load()
    expect(repoURL).toBe('https://git.example.org/fork/uruni')
    expect(licenseURL({ version: 'v1.2.3', commit: 'abc' })).toBe('https://git.example.org/fork/uruni/blob/v1.2.3/LICENSE')
  })
})
