import type { Icon } from '@phosphor-icons/react'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export interface DetailAction {
  /** Unique within the list; also the React key. */
  key: string
  label: string
  /** Used when the button shares its row (half width) and `label` would not
   * fit; a full-width button always shows `label`. */
  shortLabel?: string
  /** Always takes a full row: for a label too long for half of one
   * (Batalkan transaksi, Batalkan pemutihan) that has no short form. */
  wide?: boolean
  icon?: Icon
  onClick: () => void
  disabled?: boolean
  /** `destructive`: undoes or removes (Hapus, Batalkan transaksi) - in the
   * destructive tone, last. Anything else is a plain outline button. */
  tone?: 'destructive'
}

/**
 * The actions of one record in a detail or edit dialog, laid out one way
 * everywhere (#768): two columns of outline buttons with their full labels,
 * the destructive one last, spanning both columns when it is alone in its
 * row. Five full-width buttons took more height than the record above them;
 * this keeps every label visible and readable at the size of every other
 * button, without hiding anything in a menu.
 *
 * Never the dialog's filled button: that is the decision the dialog exists
 * for, and it always sits last, in DialogFooter (Simpan, Bayar, Ya, hapus) -
 * the bottom of the dialog, nearest the thumb. These are the other things she
 * can do with the record, each leading somewhere else.
 *
 * The caller passes only the actions that apply to this record, in reading
 * order, destructive last.
 */
export default function DetailActions({ actions }: { actions: DetailAction[] }) {
  if (actions.length === 0) return null
  return (
    <div className="grid grid-cols-2 gap-2">
      {actions.map((action, index) => (
        <ActionButton key={action.key} action={action} full={fullRow(actions, index)} />
      ))}
    </div>
  )
}

/** Whether the action at `index` takes a whole row, read in order: a wide one
 * always does; a narrow one pairs with the next narrow one, and one left
 * without a partner (the next is wide, or there is no next) takes the row. */
function fullRow(actions: DetailAction[], index: number): boolean {
  if (actions[index].wide) return true
  let start = index
  while (start > 0 && !actions[start - 1].wide) start--
  const position = index - start
  const next = actions[index + 1]
  return position % 2 === 0 && (next === undefined || next.wide === true)
}

function ActionButton({ action, full }: { action: DetailAction; full: boolean }) {
  const Icon = action.icon
  return (
    <Button
      type="button"
      size="lg"
      variant="outline"
      className={cn('min-w-0', full && 'col-span-2', action.tone === 'destructive' && 'text-destructive')}
      disabled={action.disabled}
      onClick={action.onClick}
    >
      {Icon && <Icon aria-hidden="true" />}
      {full || action.shortLabel === undefined ? (
        <span className="truncate">{action.label}</span>
      ) : (
        // Shortened to fit, the button still answers to its full label: a
        // screen reader announces "Tambah foto nota", not just "Foto nota",
        // and the visible words sit inside that name (label in name).
        <>
          <span className="sr-only">{action.label}</span>
          <span aria-hidden="true" className="truncate">
            {action.shortLabel}
          </span>
        </>
      )}
    </Button>
  )
}
