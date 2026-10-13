import { copy } from '@/copy/id'
import { cn } from '@/lib/utils'
import { shownVersion, useBuild } from '@/lib/build'

/**
 * The running build's version, a muted line under the fund name in Shell's
 * header - so an operator can tell which release a server is on from any
 * screen of a phone, without a shell on the VPS. It fits beside the header's
 * 44px logout button, so the header grows no taller. The login screen's
 * footer shows it too, so the version is readable before signing in.
 *
 * Any failure renders nothing - a footnote, never an error state.
 */
export default function AppVersion({ className }: { className?: string }) {
  const build = useBuild()
  if (!build) return null
  return (
    <p className={cn('tabular truncate text-xs leading-4 text-muted-foreground', className)}>
      {copy.settings.versionLine(shownVersion(build))}
    </p>
  )
}
