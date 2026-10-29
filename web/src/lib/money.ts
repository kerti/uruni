// The display edge for money, and the only place a rupiah figure changes
// representation.
//
// Money is int64 integer rupiah everywhere else (CLAUDE.md rule 1,
// internal/money) - the wire carries a plain integer and the client never
// does arithmetic on it. formatIDR renders; parseRupiah is the one place a
// human types a figure in.
//
// No floats on purpose: parseRupiah builds an integer from digits and never
// goes through parseFloat, so a typed "1.000.000" cannot become 1 or
// 1000000.0000001 on the way to the server.

// No currency style on purpose (#724): Intl's id-ID currency output depends
// on the engine's ICU data - Chrome and Node write "Rp\u00a01.000", Safari
// "Rp1.000" - so one app would print one figure two ways. Uruni writes the
// EYD form, "Rp" against the digits with no space, built from the grouping
// alone so no engine decides. internal/money.FormatIDR is the server's copy.
const groupFormatter = new Intl.NumberFormat('id-ID', { maximumFractionDigits: 0 })

/** Renders integer rupiah as "Rp1.000.000" for display only. */
export function formatIDR(amount: number): string {
  // A word joiner after the minus: "-" before a letter is a line-break
  // opportunity, so a narrow row could split "-Rp999.999.999.999" into "-"
  // and "Rp...", and a clipped number is a wrong number. U+2060 is invisible
  // and forbids that break; "Rp" against a digit is no break opportunity, so
  // the rest holds together on its own.
  const figure = 'Rp' + groupFormatter.format(Math.abs(amount))
  return amount < 0 ? '-\u2060' + figure : figure
}

// The grouped form a treasurer sees while typing: no "Rp", just the digits
// with Indonesian thousands separators, so an <input> can echo what she typed
// without the currency symbol fighting the caret.

/** Renders integer rupiah as "1.000.000" - the grouped form for an input's value. */
export function formatRupiahDigits(amount: number): string {
  return groupFormatter.format(amount)
}

/**
 * The largest amount Uruni accepts anywhere: Rp999.999.999.999, twelve
 * digits (the server's money.MaxAmount). Twelve digits is well under 2^53, so
 * the JS number an input parses to is always the exact figure typed - past
 * that, Number() silently rounds, a rule 1 breach at the edge.
 */
export const MAX_AMOUNT = 999_999_999_999
export const MAX_AMOUNT_DIGITS = 12

/**
 * True when whatever was typed or pasted carries more than twelve significant
 * digits, i.e. is above MAX_AMOUNT. Leading zeros do not count:
 * "0000000000001" is 1. Amount inputs use this to refuse the keystroke and
 * keep the value they hold, as they do for a non-digit.
 */
export function exceedsAmountCeiling(input: string): boolean {
  return input.replace(/\D/g, '').replace(/^0+/, '').length > MAX_AMOUNT_DIGITS
}

/**
 * Reads whatever the treasurer typed into an amount field as integer rupiah,
 * ignoring everything that is not a digit: "Rp 1.000.000", "1000000" and
 * "1 000 000" are all 1000000.
 *
 * Returns 0 for an empty or digit-free string - the wizard's "left blank
 * means don't post anything" path, not an error. Negative input is not
 * expressible: a leading minus is not a digit, which is correct for every
 * amount field - a correction is a new entry, never a negative one typed into
 * a form (CLAUDE.md rule 3).
 */
export function parseRupiah(input: string): number {
  const digits = input.replace(/\D/g, '')
  if (digits === '') return 0
  // No clamp: the inputs refuse a thirteenth digit (exceedsAmountCeiling),
  // and a figure that gets past them anyway must stay above the ceiling so
  // the server refuses it, never be pulled down to a different amount.
  return Number(digits)
}
