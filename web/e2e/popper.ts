import type { Locator, Page } from '@playwright/test'

import { expect } from './fixtures'

// Driving Radix popper content (Select, DropdownMenu, Popover). Every helper
// retries its open-then-click pair: right after a previous popper was chosen
// from, it can still be animating closed, and a click on the trigger then
// toggles it shut instead of open. The trigger is only clicked while the target
// is not already on screen.

// A Select: its combobox, then one option of the open listbox (the first one
// when `option` is omitted). Scoped to the listbox because Radix also renders a
// hidden native select whose <option> elements carry the same role and text.
// `trigger` is the combobox's accessible name, or a locator when it needs a
// scope (a dialog). Returns the chosen option's text.
export async function chooseOption(page: Page, trigger: string | Locator, option?: string | RegExp): Promise<string> {
  const combobox = typeof trigger === 'string' ? page.getByRole('combobox', { name: trigger }) : trigger
  const listbox = page.getByRole('listbox')
  const item = option === undefined ? listbox.getByRole('option').first() : listbox.getByRole('option', { name: option })
  let text = ''
  await expect(async () => {
    if (!(await item.isVisible())) await combobox.click()
    text = ((await item.textContent()) ?? '').trim()
    await item.click({ timeout: 2_000 })
  }).toPass()
  return text
}

// A DropdownMenu: the trigger button, then one of its items.
export async function chooseMenuItem(page: Page, trigger: Locator, item: string, { exact = false } = {}) {
  const menuItem = page.getByRole('menuitem', { name: item, exact })
  await expect(async () => {
    if (!(await menuItem.isVisible())) await trigger.click()
    await menuItem.click({ timeout: 2_000 })
  }).toPass()
}

// The open Popover's content (the date and month pickers), which portals out of
// whatever dialog opened it. The last one is the newest.
export function popperContent(page: Page): Locator {
  return page.locator('[data-radix-popper-content-wrapper]').last()
}
