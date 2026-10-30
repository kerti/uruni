import type { LucideIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export interface DetailAction {
  /** Unique within the list; also the React key. */
  key: string
  label: string
  /** Used when the button shares its row (half width) and `label` would not
   * fit; a full-width button always shows `label`. */
  shortLabel?: string
  icon?: LucideIcon
  onClick: () => void
  disabled?: boolean
  /** `primary`: the record's everyday action (Bayar) - filled, full width,
   * first. `destructive`: undoes or removes (Hapus, Batalkan transaksi) - in
   * the destructive tone, last. Anything else is a plain outline button. */
  tone?: 'primary' | 'destructive'
}

/**
 * The actions of one record in a detail or edit dialog, laid out one way
 * everywhere (#768): the everyday action, if there is one, full width and
 * filled; the rest in two columns of outline buttons with their full labels;
 * the destructive one last, spanning both columns when it is alone in its row.
 * Five full-width buttons took more height than the record above them; this
 * keeps every label visible and readable at the size of every other button,
 * without hiding anything in a menu.
 *
 * The caller passes only the actions that apply to this record, in reading
 * order, primary first and destructive last.
 */
export default function DetailActions({ actions }: { actions: DetailAction[] }) {
  if (actions.length === 0) return null
  const primary = actions.filter((action) => action.tone === 'primary')
  const rest = actions.filter((action) => action.tone !== 'primary')
  return (
    <div className="grid grid-cols-2 gap-2">
      {primary.map((action) => (
        <ActionButton key={action.key} action={action} full />
      ))}
      {rest.map((action, index) => (
        // An odd one out is the last one, alone in its row: it takes the row.
        <ActionButton key={action.key} action={action} full={rest.length % 2 === 1 && index === rest.length - 1} />
      ))}
    </div>
  )
}

function ActionButton({ action, full }: { action: DetailAction; full: boolean }) {
  const Icon = action.icon
  return (
    <Button
      type="button"
      size="lg"
      variant={action.tone === 'primary' ? 'default' : 'outline'}
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
