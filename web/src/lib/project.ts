import { sourceRef, type Build } from '@/lib/build'

/** Where Uruni's source lives. Not copy - a URL - so it sits here rather
 * than in copy/id.ts. */
export const repoURL = 'https://github.com/kerti/uruni'

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
