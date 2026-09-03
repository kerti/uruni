import * as React from "react"

import { cn } from "@/lib/utils"

// h-11 (44px), matching SelectTrigger and Design-System.md's minimum touch
// target. It was h-8 from the shadcn default, which made every text field
// visibly shorter than the select beside it and put the app's most-typed
// control under the touch minimum.

function Input({ className, type, ...props }: React.ComponentProps<"input">) {
  // A date/month field is full-width like every other field; what it needs
  // on top is appearance-none. iOS renders these as a native control whose
  // platform chrome and intrinsic sizing ignore the width rule, which is
  // what overran the viewport on a phone - the width was never the problem,
  // the native box was. Stripped of that, w-full behaves, and the widest
  // thing it has to hold is "September 2026" (September being the longest
  // Indonesian month name at 9 characters, ahead of Februari, November and
  // Desember at 8), which fits easily at full width. min-w-0 in the base
  // class is what lets it shrink inside a flex parent.
  //
  // The picker that opens is still the OS's: its layout and its own
  // formatting are not reachable from here.
  const isDateLike = type === "date" || type === "month" || type === "time"

  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        "h-11 w-full min-w-0 rounded-lg border border-input bg-transparent px-3 py-1 text-base transition-colors outline-none file:inline-flex file:h-6 file:border-0 file:bg-transparent file:text-sm file:font-medium file:text-foreground placeholder:text-muted-foreground focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:cursor-not-allowed disabled:bg-input/50 disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 md:text-sm dark:bg-input/30 dark:disabled:bg-input/80 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40",
        isDateLike && "appearance-none",
        className
      )}
      {...props}
    />
  )
}

export { Input }
