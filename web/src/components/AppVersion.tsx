import { copy } from '@/copy/id'
import { cn } from '@/lib/utils'
import { shownVersion, useBuild } from '@/lib/build'

/**
 * The running build's version, a muted line under the fund name in Shell's
 * header since #319 (it used to be buried at the foot of Pengaturan) - so an
 * operator can tell which release a server is on from any screen of a phone,
 * without a shell on the VPS. It fits beside the header's 44px logout
 * button, so the header grows no taller for it. The login screen's footer
 * shows it too (#356), so the version is readable before signing in.
 *
 * Any failure renders nothing - this is a footnote, never an error state.
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
