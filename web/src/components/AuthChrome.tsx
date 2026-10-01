import type { ReactNode } from 'react'

import AppVersion from '@/components/AppVersion'
import Logo from '@/components/Logo'
import { copy } from '@/copy/id'
import { useBuild } from '@/lib/build'
import { licenseURL, maintainerURL, sourceURL } from '@/lib/project'

const text = copy.auth.chrome

const footerLink =
  'inline-flex min-h-11 items-center underline-offset-4 hover:text-foreground hover:underline rounded-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50'

/**
 * The frame around both signed-out screens, Login and Register (#356,
 * #364): the logo with the tagline and promise above the card, and the
 * version, source, licence and maintainer below it. The card itself - the
 * form - is the caller's.
 *
 * Register shows it too because it is the first screen a fresh install
 * puts in front of anyone, and AGPL-3.0 section 13 offers a network user
 * the source of the version they are using from the start.
 */
export default function AuthChrome({ children }: { children: ReactNode }) {
  const build = useBuild()

  return (
    <main className="flex min-h-dvh flex-col items-center justify-center gap-6 p-6">
      <div className="flex flex-col items-center gap-3 text-center">
        <Logo />
        <div className="flex flex-col gap-1">
          <p className="font-semibold">{text.tagline}</p>
          <p className="text-sm text-balance text-muted-foreground">{text.promise}</p>
        </div>
      </div>
      {children}
      {/* Under the card, not in it: none of this is part of signing in. Each
          link is min-h-11 for the 44px touch target, and opens in a new tab
          so a half-typed form is not lost. */}
      <footer className="flex w-full max-w-sm flex-col items-center gap-1 text-xs text-muted-foreground">
        <AppVersion />
        <ul className="flex flex-wrap items-center justify-center gap-x-4">
          <li>
            <a className={footerLink} href={sourceURL(build)} target="_blank" rel="noreferrer">
              {text.sourceCode}
            </a>
          </li>
          <li>
            <a className={footerLink} href={licenseURL(build)} target="_blank" rel="noreferrer">
              {text.license}
            </a>
          </li>
          <li>
            <a className={footerLink} href={maintainerURL} target="_blank" rel="noreferrer">
              {text.maintainer}
            </a>
          </li>
        </ul>
      </footer>
    </main>
  )
}
