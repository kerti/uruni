import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

/** Where to look for the trigger. Two fields can share a label - a row
 * being edited and the add form below it both say "Golongan" - so a caller
 * passes the row (or the form) to disambiguate. The *listbox* is always
 * found on `screen`: Radix portals it to the body, outside any scope. */
type Scope = { getByRole: typeof screen.getByRole }

/**
 * Helpers for the themed Select (components/ui/select.tsx). Its options live
 * in a portal that only exists while the popup is open, so a test cannot
 * query them the way it queried a native <select>'s <option> children -
 * it has to open the thing first, which is also what the treasurer does.
 *
 * `name` is the trigger's accessible name (its aria-label, i.e. the field's
 * own Label text).
 */
export async function openSelect(name: string, scope: Scope = screen): Promise<HTMLElement> {
  await userEvent.click(scope.getByRole('combobox', { name }))
  return screen.getByRole('listbox')
}

/** Opens the named select and chooses one option by its visible text. */
export async function chooseOption(name: string, option: string, scope: Scope = screen): Promise<void> {
  const listbox = await openSelect(name, scope)
  await userEvent.click(within(listbox).getByRole('option', { name: option }))
}

/** What the named select currently shows. The themed Select's trigger
 * renders the chosen item's text, not its value, so a test that used to
 * read `select.value` (an id) reads the name here instead. */
export function selectedOptionName(name: string, scope: Scope = screen): string {
  return scope.getByRole('combobox', { name }).textContent ?? ''
}

/** The visible text of every option the named select offers, with the popup
 * left open for any further assertion. */
export async function selectOptionNames(name: string, scope: Scope = screen): Promise<string[]> {
  const listbox = await openSelect(name, scope)
  return within(listbox)
    .getAllByRole('option')
    .map((option) => option.textContent ?? '')
}
