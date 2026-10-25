import type { Locator, Page } from '@playwright/test'

// A whole-card button (Settings, the roster) has no aria-label: its accessible
// name is a visually hidden verb followed by every visible word ("Ubah Kotak
// kas Tunai"), so it holds all the visible text (WCAG 2.5.3, Label in Name).
// Matched by structure: the name starts with the verb AND the card shows `name`
// as the whole text of one element, so "Warga Dua" never matches the card of
// "Warga Dua Baru" (Playwright's own `name` is a substring match).
export function cardButton(page: Page, verb: string, name: string): Locator {
  const start = new RegExp(`^${verb.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} `)
  return page.getByRole('button', { name: start }).filter({ has: page.getByText(name, { exact: true }) })
}
