// The one way an ApiError becomes words (ADR-014): the wire code is looked up,
// never the English wire message shown.

import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'

const shared: Partial<Record<string, string>> = copy.common.errors

/** A wire code the shared copy map knows. A screen that branches on a code
 * names it through this type, so a misspelt code does not compile. */
export type ErrorCode = keyof typeof copy.common.errors

/** A screen's own words for some codes, checked before the shared map - e.g.
 * a feature's `errors` group in copy/id.ts. */
export type ErrorMessages = Partial<Record<string, string>>

/** The Indonesian sentence for an error: the screen's own words for its code,
 * else the shared map's, else the warm generic one. */
export function errorText(err: ApiError, messages?: ErrorMessages): string {
  return messages?.[err.code] ?? shared[err.code] ?? copy.common.unknownError
}

/** Whether a thrown value is an ApiError carrying `code`. */
export function hasCode(err: unknown, code: ErrorCode): boolean {
  return err instanceof ApiError && err.code === code
}

/** For a `.catch` on a read whose 404 is an answer, not a failure (a claim
 * deleted meanwhile, a fund never reconciled): null for `not_found`, anything
 * else rethrown. */
export function nullIfNotFound(err: unknown): null {
  if (hasCode(err, 'not_found')) return null
  throw err
}
