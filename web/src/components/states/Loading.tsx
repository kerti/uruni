import { LoaderCircle } from 'lucide-react'

import { copy } from '@/copy/id'

/** A small, calm inline loading indicator - used wherever a screen waits on an /api/* call. */
export default function Loading({ label = copy.common.loading }: { label?: string }) {
  return (
    <p role="status" className="flex items-center gap-2 text-muted-foreground">
      <LoaderCircle aria-hidden="true" className="animate-spin" />
      {label}
    </p>
  )
}
