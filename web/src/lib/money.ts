// The display edge for money, and the only place a rupiah figure changes
// representation.
//
// Money is int64 integer rupiah everywhere else in this codebase (CLAUDE.md
// rule 1, internal/money) - the wire carries a plain integer and the client
// never does arithmetic on it. These two functions exist so a screen never
// hand-rolls either direction: formatIDR for rendering, parseRupiah for the
// one place a human types a figure in.
//
// There are no floats here on purpose. parseRupiah reads digits and builds
// an integer from them; it never goes through parseFloat, so a typed
// "1.000.000" cannot become 1 or 1000000.0000001 on the way to the server.

const idrFormatter = new Intl.NumberFormat('id-ID', {
  style: 'currency',
  currency: 'IDR',
  minimumFractionDigits: 0,
  maximumFractionDigits: 0,
})

/** Renders integer rupiah as "Rp 1.000.000" for display only. */
export function formatIDR(amount: number): string {
  return idrFormatter.format(amount)
}

// The grouped form a treasurer sees while typing: no "Rp", just the digits
// with Indonesian thousands separators, so an <input> can echo what she
// typed back to her without the currency symbol fighting the caret.
const groupFormatter = new Intl.NumberFormat('id-ID', { maximumFractionDigits: 0 })

/** Renders integer rupiah as "1.000.000" - the grouped form for an input's value. */
export function formatRupiahDigits(amount: number): string {
  return groupFormatter.format(amount)
}

/**
 * Reads whatever the treasurer typed into an amount field as integer
 * rupiah, ignoring everything that is not a digit: "Rp 1.000.000",
 * "1000000" and "1 000 000" are all 1000000.
 *
 * Returns 0 for an empty or digit-free string - the wizard's
 * "left blank means don't post anything" path (PostOpeningBalance's own
 * zero-amount contract), not an error. Negative input is not expressible:
 * a leading minus is simply not a digit, which is correct for every amount
 * field in the app - a correction is a new entry, never a negative one
 * typed into a form (CLAUDE.md rule 3).
 */
export function parseRupiah(input: string): number {
  const digits = input.replace(/\D/g, '')
  if (digits === '') return 0
  return Number(digits)
}
