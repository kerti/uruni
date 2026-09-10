import * as React from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { X } from "lucide-react"

import { cn } from "@/lib/utils"

// The dialog primitive every non-posting edit in this milestone uses
// (M6.28, ADR-032 "Every non-posting edit is a dialog"), proven on the
// Lokasi section of settings.
//
// A modal, styled as a bottom sheet rather than shadcn's default centred
// card: `max-h-[85dvh]` (dvh, not vh, for iOS's collapsing toolbars),
// internal scroll on the body only (the header stays put), bottom padding
// that clears the home indicator via env(safe-area-inset-bottom). A centred
// modal with the phone keyboard open puts the field under the thumb row -
// anchoring to the bottom keeps it reachable.
//
// Radix, already a dependency (select.tsx, label.tsx): `import { Dialog as
// DialogPrimitive } from "radix-ui"`, no `@radix-ui/react-dialog` and no
// `vaul` (CLAUDE.md rule 7 - one dependency, not two). Radix gives focus
// trap, focus restore and background inerting for free; this file only
// styles it.
//
// This component owns no modal state of its own - the caller controls
// `open`/`onOpenChange` from a search param (see `lib/useDialogParam.ts`),
// so Esc, a backdrop tap and the close button all funnel through the same
// `onOpenChange(false)` Radix already calls for each of them.
//
// No nested dialogs, ever, and never `window.confirm()` - a destructive
// confirm swaps this same dialog's footer in place instead (see
// Locations.tsx).

/** The modal root. Re-exported bare - every prop is Radix's own. */
const Dialog = DialogPrimitive.Root

function DialogOverlay({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 z-50 bg-foreground/40 data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out",
        className,
      )}
      {...props}
    />
  )
}

interface DialogContentProps extends React.ComponentProps<typeof DialogPrimitive.Content> {
  /** Radix requires a Title for a11y - every sheet has one, in the header
   * next to the close button, never left implicit. */
  title: string
  /** copy.common.close - passed in rather than imported here, the same
   * idiom as select.tsx's SelectValue placeholder: this file stays a
   * generic primitive with no copy of its own. */
  closeLabel: string
}

function DialogContent({ className, children, title, closeLabel, ...props }: DialogContentProps) {
  return (
    <DialogPrimitive.Portal>
      <DialogOverlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        className={cn(
          "fixed inset-x-0 bottom-0 z-50 flex max-h-[85dvh] flex-col gap-4 rounded-t-2xl border-t border-border bg-card px-4 pt-4 shadow-floating outline-none data-[state=open]:animate-sheet-in data-[state=closed]:animate-sheet-out",
          className,
        )}
        {...props}
      >
        <div className="flex shrink-0 items-start justify-between gap-3">
          <DialogPrimitive.Title className="pt-2 text-base font-semibold">{title}</DialogPrimitive.Title>
          <DialogPrimitive.Close
            aria-label={closeLabel}
            className="-mr-2 flex size-11 shrink-0 items-center justify-center rounded-lg text-muted-foreground transition-colors outline-none hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <X aria-hidden="true" className="size-5" />
          </DialogPrimitive.Close>
        </div>
        {/* The only part that scrolls - the header and its close button
            stay reachable no matter how tall the body gets. */}
        <div className="flex-1 overflow-y-auto pb-[calc(env(safe-area-inset-bottom)+1rem)]">{children}</div>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

export { Dialog, DialogContent }
