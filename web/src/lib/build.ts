import { useEffect, useState } from 'react'

/** What /healthz says about the running binary (ADR-018's operator contract). */
export type Build = { version: string; commit: string }

/**
 * Reads the running build from /healthz, the same unauthenticated
 * {version, commit} the readiness checks use, rather than a new route: the
 * SPA is built before the binary is stamped, so the version can only be
 * learned at runtime. null until it answers, and null for good if it fails -
 * everything built on this is a footnote, never an error state.
 */
export function useBuild(): Build | null {
  const [build, setBuild] = useState<Build | null>(null)

  useEffect(() => {
    let cancelled = false
    fetch('/healthz')
      .then((res) => (res.ok ? res.json() : null))
      .then((body: { version?: string; commit?: string } | null) => {
        if (cancelled || !body?.version) return
        setBuild({ version: body.version, commit: body.commit ?? 'unknown' })
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  return build
}

function knownCommit(commit: string): boolean {
  return commit !== '' && commit !== 'unknown'
}

/** A tagged build shows its version; an untagged one is always `dev`, where
 * the short commit is the only thing naming what runs. */
export function shownVersion({ version, commit }: Build): string {
  return version === 'dev' && knownCommit(commit) ? `${version} (${commit.slice(0, 7)})` : version
}

/** The git ref the running binary was built from, for links into the
 * repository: the tag when there is one, else the commit, else main.
 * AGPL-3.0 section 13 asks that network users be offered the source of the
 * version they are using, not whatever main is today. */
export function sourceRef(build: Build | null): string {
  if (!build) return 'main'
  if (build.version !== 'dev') return build.version
  return knownCommit(build.commit) ? build.commit : 'main'
}
