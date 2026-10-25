/**
 * Name matcher for the whole-card buttons (Settings, the roster). Such a
 * button has no aria-label: its accessible name is built from its content,
 * a visually hidden verb first and then every visible word ("Ubah Kotak kas
 * Tunai"), so it holds all the visible text (WCAG 2.5.3, Label in Name).
 *
 * Matching is by structure, not by a prefix of the string: it starts with
 * the verb AND the card shows `name` as the whole text of one element. That
 * keeps "Warga Dua" from matching the card of "Warga Dua Baru", and is
 * independent of how the pieces after the name are joined.
 *
 *   screen.getByRole('button', { name: cardName(text.editVerb, 'Bank Jago') })
 */
export function cardName(verb: string, name: string): (accessibleName: string, element: Element) => boolean {
  return (accessibleName, element) =>
    accessibleName.startsWith(`${verb} `) &&
    Array.from(element.querySelectorAll('*')).some((el) => el.children.length === 0 && el.textContent === name)
}
