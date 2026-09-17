/**
 * What an `?edit=` value names, from one section's point of view.
 *
 * `foreign` is the load-bearing case, and the reason this parser is shared
 * rather than written per section. Pengaturan renders every section against
 * one URL, and four of them own an `?edit=` dialog, so "not mine" and "mine
 * but broken" must be told apart: a section that cleared everything it could
 * not parse would strip the param its neighbour's open dialog is living on
 * and close it mid-edit. Anything carrying another section's prefix - or no
 * prefix at all - is `foreign` and is left exactly where it is. Only a value
 * with this section's own prefix and an unusable rest is `invalid`, and only
 * that is ever cleared.
 */
export type DialogTarget =
  | { kind: 'new' }
  | { kind: 'edit'; id: number }
  | { kind: 'foreign' }
  | { kind: 'invalid' }

/**
 * Parses `?edit=<prefix>:<rest>` for the section owning `prefix`.
 *
 * `<prefix>:new` is the add dialog; `<prefix>:<positive integer>` names an
 * existing row. A section that owns no add dialog, or no edit dialog, simply
 * never opens that value and treats it as unusable when it arrives by hand -
 * the parser does not need to know which sections those are.
 */
export function parseDialogTarget(prefix: string, value: string | null): DialogTarget {
  if (value === null) return { kind: 'foreign' }
  const separator = value.indexOf(':')
  if (separator < 0) return { kind: 'foreign' }
  if (value.slice(0, separator) !== prefix) return { kind: 'foreign' }

  const rest = value.slice(separator + 1)
  if (rest === 'new') return { kind: 'new' }

  const id = Number(rest)
  return Number.isInteger(id) && id > 0 ? { kind: 'edit', id } : { kind: 'invalid' }
}
