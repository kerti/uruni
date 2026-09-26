import { useEffect, useState } from 'react'

import { copy } from '@/copy/id'

/**
 * The running build's version, last thing on Pengaturan - so an operator
 * can tell which release a server is on from a phone, without a shell on
 * the VPS. It reads /healthz, the same unauthenticated {version, commit}
 * the readiness checks use (ADR-018's operator contract), rather than a
 * new route: the SPA is built before the binary is stamped, so the version
 * can only be learned at runtime.
 *
 * A tagged build shows its version; an untagged one is always `dev`, where
 * the short commit is the only thing naming what runs. Any failure renders
 * nothing - this is a footnote, never an error state.
 */
export default function AppVersion() {
  const [label, setLabel] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    fetch('/healthz')
      .then((res) => (res.ok ? res.json() : null))
      .then((body: { version?: string; commit?: string } | null) => {
        if (cancelled || !body?.version) return
        const { version, commit } = body
        const shown = version === 'dev' && commit && commit !== 'unknown' ? `${version} (${commit.slice(0, 7)})` : version
        setLabel(copy.settings.versionLine(shown))
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [])

  if (!label) return null
  return <p className="tabular text-center text-sm text-muted-foreground">{label}</p>
}
