import { sourceRef, type Build } from '@/lib/build'

/** Where Uruni's source lives. Not copy - a URL - so it sits here rather
 * than in copy/id.ts.
 *
 * A build-time value (#521): the Dockerfile's URUNI_SOURCE_URL arg becomes
 * VITE_SOURCE_URL for the web build, so a fork that distributes a modified
 * build points its users at its own source (AGPL-3.0 section 13), not at
 * upstream. Unset, it is upstream. */
export const repoURL = (import.meta.env.VITE_SOURCE_URL || 'https://github.com/kerti/uruni').replace(/\/+$/, '')

/** The maintainer's own site, credited on the login screen (#356). */
export const maintainerURL = 'https://radityakertiyasa.com'

/** The source tree of the build that is running (see sourceRef). */
export function sourceURL(build: Build | null): string {
  return `${repoURL}/tree/${sourceRef(build)}`
}

/** The LICENSE file as it stood in the build that is running. */
export function licenseURL(build: Build | null): string {
  return `${repoURL}/blob/${sourceRef(build)}/LICENSE`
}

/** The third-party licence notices, served by the Go binary itself (#521) -
 * same origin, so a fork's own source URL never changes it. The footer opens
 * it in a new tab like the other links, so a half-typed form is not lost. */
export const thirdPartyNoticesPath = '/third-party-notices'
