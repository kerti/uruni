import * as React from "react"
import { Select as SelectPrimitive } from "radix-ui"
import { Check, ChevronDown } from "lucide-react"

import { cn } from "@/lib/utils"

// A themed select, in the design system's own shapes instead of the
// browser's. The native <select> the pickers used until now rendered the
// platform's list - iOS's wheel, Chrome's grey menu - which is the one
// control in the app that never looked like the rest of it.
//
// Radix's Select, not a hand-rolled listbox: `radix-ui` is already a
// dependency (button.tsx's Slot, label.tsx's Label), so this is styling
// something we already ship rather than a new package, and it brings the
// keyboard model, focus management and typeahead that a hand-rolled one
// would get wrong quietly.
//
// Trigger height is h-11 (44px), matching Input and Design-System.md's
// minimum touch target - the two now agree, which they did not while Input
// was h-8.
//
// Two rules for callers. Pass `value` as a string from the very first
// render - '' for "nothing chosen yet", never undefined - or Radix flips
// from uncontrolled to controlled the moment a fetch resolves and warns
// about it. And always give SelectValue explicit children (the
// chosen option's text). Radix learns an item's label only once that item
// has mounted, and items live in a portal that does not exist until the
// popup is first opened - so a bare <SelectValue /> renders an empty field
// for a value that was set programmatically and never touched, which is
// every pre-filled form in this app.

function Select({ ...props }: React.ComponentProps<typeof SelectPrimitive.Root>) {
  return <SelectPrimitive.Root data-slot="select" {...props} />
}

/**
 * What the closed trigger shows. Deliberately NOT Radix's own
 * SelectPrimitive.Value: that component renders the selected item's label by
 * portalling it out of the item, and items live in a popup that does not
 * exist until it is first opened - so a field whose value arrives from a
 * fetch (every pre-filled form here) sits visibly blank until touched.
 *
 * So the caller passes the text. `placeholder` shows when there is none.
 */
function SelectValue({ placeholder, children }: { placeholder?: string; children?: React.ReactNode }) {
  const empty = children === undefined || children === null || children === ''
  return (
    <span data-slot="select-value" className={cn("truncate", empty && "text-muted-foreground")}>
      {empty ? placeholder : children}
    </span>
  )
}

function SelectTrigger({ className, children, ...props }: React.ComponentProps<typeof SelectPrimitive.Trigger>) {
  return (
    <SelectPrimitive.Trigger
      data-slot="select-trigger"
      className={cn(
        "flex h-11 w-full items-center justify-between gap-2 rounded-lg border border-input bg-transparent px-3 text-base outline-none transition-colors focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 data-[placeholder]:text-muted-foreground md:text-sm [&_svg]:pointer-events-none [&_svg]:shrink-0",
        className,
      )}
      {...props}
    >
      {children}
      <SelectPrimitive.Icon asChild>
        <ChevronDown aria-hidden="true" className="size-4 text-muted-foreground" />
      </SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  )
}

function SelectContent({ className, children, position = "popper", ...props }: React.ComponentProps<typeof SelectPrimitive.Content>) {
  return (
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content
        data-slot="select-content"
        position={position}
        className={cn(
          "relative z-50 max-h-(--radix-select-content-available-height) min-w-[8rem] overflow-y-auto rounded-xl border border-border bg-card text-foreground shadow-floating",
          position === "popper" && "w-full min-w-(--radix-select-trigger-width) translate-y-1",
          className,
        )}
        {...props}
      >
        <SelectPrimitive.Viewport className="p-1">{children}</SelectPrimitive.Viewport>
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  )
}

function SelectItem({ className, children, ...props }: React.ComponentProps<typeof SelectPrimitive.Item>) {
  return (
    <SelectPrimitive.Item
      data-slot="select-item"
      className={cn(
        // min-h-11 here too: a 44px target in the list, not only on the
        // trigger that opens it.
        "relative flex min-h-11 w-full cursor-default items-center gap-2 rounded-lg py-2 pr-8 pl-3 text-base outline-none select-none focus:bg-secondary data-[disabled]:pointer-events-none data-[disabled]:opacity-50 md:text-sm",
        className,
      )}
      {...props}
    >
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
      <span className="absolute right-3 flex items-center">
        <SelectPrimitive.ItemIndicator>
          <Check aria-hidden="true" className="size-4 text-primary" />
        </SelectPrimitive.ItemIndicator>
      </span>
    </SelectPrimitive.Item>
  )
}

export { Select, SelectContent, SelectItem, SelectTrigger, SelectValue }
