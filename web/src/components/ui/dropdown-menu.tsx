import * as React from "react"
import { DropdownMenu as DropdownMenuPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"

// A themed dropdown menu, same shape as select.tsx and dialog.tsx: Radix
// gives the popup positioning, focus management and typeahead, this file
// only styles it. First use is ReceiptDialog.tsx's per-photo "more" menu
// (#154 follow-up) - a menu button on the photo itself, replacing the
// button row that used to sit under it.
//
// Rendered in a Portal, same as SelectContent, so it floats above whatever
// dialog it opened from rather than being clipped by that dialog's own
// overflow-y-auto. `z-[60]` (Dialog's own content is z-50) is what keeps it
// on top when a ReceiptDialog is open underneath. (`z-60` is not a
// standard Tailwind scale step, hence the bracket value.)

const DropdownMenu = DropdownMenuPrimitive.Root
const DropdownMenuTrigger = DropdownMenuPrimitive.Trigger

function DropdownMenuContent({
  className,
  sideOffset = 4,
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Content>) {
  return (
    <DropdownMenuPrimitive.Portal>
      <DropdownMenuPrimitive.Content
        data-slot="dropdown-menu-content"
        sideOffset={sideOffset}
        className={cn(
          "z-[60] min-w-[9rem] overflow-hidden rounded-xl border border-border bg-card p-1 text-foreground shadow-floating outline-none data-[state=open]:animate-dialog-in data-[state=closed]:animate-dialog-out",
          className,
        )}
        {...props}
      />
    </DropdownMenuPrimitive.Portal>
  )
}

/**
 * One row. `variant="destructive"` is the terracotta/destructive tone the
 * app already uses for a delete action (never alarm-red) - the same tone
 * Button's own `destructive` variant carries, just as a menu row instead of
 * a button. `min-h-11` keeps every row a 44px target even though the menu
 * itself is compact.
 */
function DropdownMenuItem({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<typeof DropdownMenuPrimitive.Item> & {
  variant?: "default" | "destructive"
}) {
  return (
    <DropdownMenuPrimitive.Item
      data-slot="dropdown-menu-item"
      data-variant={variant}
      className={cn(
        "relative flex min-h-11 w-full cursor-default items-center gap-2 rounded-lg px-3 text-base outline-none select-none focus:bg-secondary data-[disabled]:pointer-events-none data-[disabled]:opacity-50 md:text-sm",
        variant === "destructive" && "text-destructive focus:bg-destructive/10",
        className,
      )}
      {...props}
    />
  )
}

export { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger }
