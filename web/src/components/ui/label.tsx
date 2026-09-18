import * as React from "react"
import { Label as LabelPrimitive } from "radix-ui"

import { cn } from "@/lib/utils"

// text-xs and muted rather than shadcn's text-sm foreground (#235): a form
// here is a stack of short fields, and at the default weight the labels
// competed with the values they name - on the record form, "Jumlah" read as
// loud as the amount beneath it. Muted and one step smaller keeps every cue
// and lets the values carry the screen. The values themselves stay text-base
// (Input's own size), so the contrast between a label and its answer is what
// does the work, not the label's absence.

function Label({
  className,
  ...props
}: React.ComponentProps<typeof LabelPrimitive.Root>) {
  return (
    <LabelPrimitive.Root
      data-slot="label"
      className={cn(
        "flex items-center gap-2 text-xs leading-none font-medium text-muted-foreground select-none group-data-[disabled=true]:pointer-events-none group-data-[disabled=true]:opacity-50 peer-disabled:cursor-not-allowed peer-disabled:opacity-50",
        className
      )}
      {...props}
    />
  )
}

export { Label }
