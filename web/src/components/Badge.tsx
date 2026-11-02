import type { Icon } from '@phosphor-icons/react'
import type { ReactNode } from 'react'

import { cn } from '@/lib/utils'

/** The three gentle tones a status can take (Design-System.md): settled,
 * worth a look, and neither. No alarm red - a normal discrepancy or an unpaid
 * month is terracotta at most. */
export type BadgeTone = 'success' | 'attention' | 'neutral'

const toneClass: Record<BadgeTone, string> = {
  success: 'bg-success-soft text-success',
  attention: 'bg-attention-soft text-attention-text',
  neutral: 'bg-muted text-muted-foreground',
}

/**
 * The one status pill: a word (Lunas, Cocok, Berjalan, Tunggakan 3 bulan) on
 * a soft fill, at the Label size Design-System.md gives chips. It was eleven
 * inline copies at two sizes - the dues screen's at 14px with an icon, the
 * rest at 12px without - so the same kind of word looked different screen to
 * screen. An icon is optional and sits at the text's own height.
 */
export function Badge({
  tone,
  icon: IconComponent,
  className,
  children,
}: {
  tone: BadgeTone
  icon?: Icon
  /** Layout only (alignment, a ring for a variant); the look is the tone's. */
  className?: string
  children: ReactNode
}) {
  return (
    <span
      className={cn(
        'inline-flex shrink-0 items-center gap-1 rounded-lg px-2.5 py-0.5 text-[0.8125rem] leading-5 font-medium whitespace-nowrap',
        toneClass[tone],
        className,
      )}
    >
      {IconComponent && <IconComponent aria-hidden="true" className="size-3.5" />}
      {children}
    </span>
  )
}
