import * as React from "react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { X } from "lucide-react"

import { useKeyboardInset } from "@/lib/useKeyboardInset"
import { cn } from "@/lib/utils"

// The dialog primitive every non-posting edit in this milestone uses
// (M6.28, ADR-032 "Every non-posting edit is a dialog"), proven on the
// Lokasi section of settings.
//
// A centred modal with the same shape as Balances' dialog (kerti/balances-v2,
// frontend/src/components/ui/dialog.tsx): a card inset 1rem from each side,
// a header, the body, and a muted footer strip holding the decision's
// buttons. The first build shipped a bottom sheet; it read wrong on a phone
// next to the rest of the app.
//
// Three things differ from Balances on purpose:
// - The footer's two buttons sit side by side at every width, half the row
//   each. Balances stacks them full-width below `sm`; with only ever two, a
//   row reads better and each is still a wide thumb target.
// - The close button is a 44px target (Design-System.md's minimum), not
//   `icon-sm`'s 28px.
// - With the on-screen keyboard open, the dialog centres itself in what is
//   still visible (lib/useKeyboardInset.ts). `top-1/2` is half the *layout*
//   viewport, which iOS does not shrink for the keyboard, so without this
//   the footer lands behind it.
//
// The content box is its own scroller and carries its own padding, so a
// focused field's ring - drawn outside its border box - is never clipped by
// the scroll edge.
//
// Radix, already a dependency (select.tsx, label.tsx) - no
// `@radix-ui/react-dialog`, no `vaul`, no `tw-animate-css`; the motion is
// two keyframes in index.css, opacity and scale only. Radix gives focus
// trap, focus restore and background inerting; this file only styles it.
//
// The caller controls `open`/`onOpenChange` from a search param
// (lib/useDialogParam.ts). No nested dialogs, ever, and never
// `window.confirm()` - a destructive confirm takes over this same dialog's
// footer instead (see Locations.tsx).

/** The modal root. Re-exported bare - every prop is Radix's own. */
const Dialog = DialogPrimitive.Root

function DialogOverlay({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Overlay>) {
  return (
    <DialogPrimitive.Overlay
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 isolate z-50 bg-black/10 supports-backdrop-filter:backdrop-blur-xs data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out",
        className,
      )}
      {...props}
    />
  )
}

interface DialogContentProps extends React.ComponentProps<typeof DialogPrimitive.Content> {
  /** copy.common.close - passed in rather than imported here, the same
   * idiom as select.tsx's SelectValue placeholder: this file stays a
   * generic primitive with no copy of its own. */
  closeLabel: string
}

function DialogContent({ className, children, closeLabel, style, ...props }: DialogContentProps) {
  const { inset, height } = useKeyboardInset()
  // The visible area's bottom edge sits `inset` above the layout viewport's;
  // its centre is half the visible height above that. Only mounted while
  // open (Portal), so nothing listens otherwise.
  const keyboardStyle: React.CSSProperties | undefined =
    inset > 0 ? { top: window.innerHeight - inset - height / 2, maxHeight: height - 32 } : undefined

  return (
    <DialogPrimitive.Portal>
      <DialogOverlay />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        // No DialogDescription on these forms - the title says it all - so
        // opt out of Radix's describedby warning explicitly.
        aria-describedby={undefined}
        className={cn(
          "fixed top-1/2 left-1/2 z-50 grid max-h-[90dvh] w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-4 overflow-y-auto rounded-xl bg-popover p-4 text-popover-foreground ring-1 ring-foreground/10 outline-none sm:max-w-sm data-[state=open]:animate-dialog-in data-[state=closed]:animate-dialog-out",
          className,
        )}
        style={keyboardStyle ? { ...style, ...keyboardStyle } : style}
        {...props}
      >
        {children}
        <DialogPrimitive.Close
          aria-label={closeLabel}
          className="absolute top-1 right-1 flex size-11 items-center justify-center rounded-lg text-muted-foreground transition-colors outline-none hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          <X aria-hidden="true" className="size-4" />
        </DialogPrimitive.Close>
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  )
}

/** Title block. Right padding keeps a long title clear of the close button. */
function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return <div data-slot="dialog-header" className={cn("flex flex-col gap-2 pr-10", className)} {...props} />
}

function DialogTitle({ className, ...props }: React.ComponentProps<typeof DialogPrimitive.Title>) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn("font-heading text-base leading-none font-medium", className)}
      {...props}
    />
  )
}

/** The button strip. Bleeds to the content box's edges (hence the negative
 * margins matching its p-4), so it must be the last thing in the dialog.
 * Children go in reading order - secondary first, primary last - side by
 * side, splitting the row equally at every width: a decision here is two
 * buttons, and on a phone each is a thumb target, so each gets as much
 * width as the row has. The primary is told apart by its fill, not its
 * size. */
function DialogFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "-mx-4 -mb-4 flex gap-2 rounded-b-xl border-t border-border bg-muted/50 p-4 *:flex-1",
        className,
      )}
      {...props}
    />
  )
}

export { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle }
