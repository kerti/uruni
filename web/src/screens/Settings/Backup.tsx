import { Download } from 'lucide-react'

import { Button } from '@/components/ui/button'
import ErrorState from '@/components/states/ErrorState'
import { copy } from '@/copy/id'
import { downloadBackup } from '@/lib/backup'
import { useApi } from '@/lib/useApi'

const text = copy.settings.backup

/**
 * The Cadangan card (M6.37, #323, ADR-012): one button that downloads the
 * whole fund as a zip - uruni.json plus every receipt photo. No dialog, no
 * list to browse - unlike every section above it on this screen, there is
 * nothing here to edit, only one action to take, so a card with a body
 * line and a button is the whole section.
 *
 * useApi<void>() gives this the same idle/loading/error shape every other
 * write action on this screen already uses, even though the call itself is
 * a GET - the button still needs to disable itself while the zip is being
 * built and show the same ErrorState a failed save would.
 */
export default function Backup() {
  const [state, run] = useApi<void>()
  const busy = state.status === 'loading'

  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <h2 className="text-base font-semibold">{text.heading}</h2>
        <p className="text-sm text-muted-foreground">{text.body}</p>
      </div>

      <Button
        type="button"
        variant="outline"
        className="h-11 w-full justify-center gap-2"
        disabled={busy}
        onClick={() => void run(downloadBackup)}
      >
        <Download aria-hidden="true" className="size-4" />
        {busy ? text.downloading : text.download}
      </Button>

      {state.status === 'error' && state.error && <ErrorState error={state.error} onRetry={() => void run(downloadBackup)} />}
    </section>
  )
}
