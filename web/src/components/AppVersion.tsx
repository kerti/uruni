import { useEffect, useState } from 'react'

import { copy } from '@/copy/id'

/**
 * The running build's version, a muted line under the fund name in Shell's
 * header since #319 (it used to be buried at the foot of Pengaturan) - so an
 * operator can tell which release a server is on from any screen of a phone,
 * without a shell on the VPS. It fits beside the header's 44px logout
 * button, so the header grows no taller for it. It reads /healthz, the same unauthenticated {version, commit}
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
  return <p className="tabular truncate text-xs leading-4 text-muted-foreground">{label}</p>
}
