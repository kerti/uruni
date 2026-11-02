import { CaretRight } from '@phosphor-icons/react'

/**
 * The chevron at the end of a card row that takes you to another screen.
 * A row that opens a dialog over this one carries none, so the arrow keeps
 * meaning "this goes somewhere". One size and one ink everywhere it appears.
 */
export default function RowChevron() {
  return <CaretRight aria-hidden="true" className="size-5 shrink-0 opacity-70" />
}
