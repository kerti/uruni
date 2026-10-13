import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

/** Where to look for the trigger. Two fields can share a label - a row being
 * edited and the add form below it both say "Golongan" - so a caller passes
 * the row (or the form) to disambiguate. The *listbox* is always found on
 * `screen`: Radix portals it to the body, outside any scope. */
type Scope = { getByRole: typeof screen.getByRole }

/**
 * Helpers for the themed Select (components/ui/select.tsx). Its options live
 * in a portal that exists only while the popup is open, so a test cannot
 * query them as it did a native <select>'s <option> children - it opens the
 * thing first, as the treasurer does.
 *
 * `name` is the trigger's accessible name (its aria-label, the field's Label
 * text).
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

/** What the named select currently shows. The trigger renders the chosen
 * item's text, not its value, so this reads the name, not an id. */
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
