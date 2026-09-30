import { useEffect, useState } from 'react'
import { Download } from 'lucide-react'

import { Button } from '@/components/ui/button'
import ErrorState from '@/components/states/ErrorState'
import Loading from '@/components/states/Loading'
import { copy } from '@/copy/id'
import { ApiError } from '@/lib/api'
import { downloadBackup, downloadStoredBackup, listStoredBackups } from '@/lib/backup'
import { formatIsoDate } from '@/lib/dates'
import { useApi } from '@/lib/useApi'
import type { StoredBackup } from '@/lib/backup'

const text = copy.settings.backup

/**
 * The Cadangan card: download now (M6.37, #323, ADR-012) plus the auto-list
 * of what the server already keeps on its own (M6.38, #324, ADR-013). Two
 * separate actions, so two separate pieces - the on-demand button first
 * (unchanged since #323), then the list, its own small heading telling them
 * apart rather than one undifferentiated block of controls.
 *
 * useApi<void>() gives the top button the same idle/loading/error shape
 * every other write action on this screen already uses, even though the
 * call itself is a GET - it still needs to disable itself while the zip is
 * being built and show the same ErrorState a failed save would.
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

      <AutoBackupList />
    </section>
  )
}

/** "512" -> "512 B", "51200" -> "50 KB", "5242880" -> "5 MB". Whole numbers
 * only - a dump's size is a rough sense of "is this normal", never a figure
 * anyone reads precisely, so a decimal place would be false precision. */
function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`
  return `${Math.round(bytes / (1024 * 1024))} MB`
}

function kindLabel(kind: StoredBackup['kind']): string {
  return kind === 'pre-restore' ? text.kindPreRestore : text.kindDaily
}

/**
 * The list of server-side dumps ADR-013's own scheduler (and the boot-time
 * format-version check) already wrote to URUNI_BACKUP_DIR - dense rows, no
 * dialog, nothing here to edit: every row is history, not a record this
 * screen can change. Each row's own download sits as plain ink beside the
 * row's text (#257/ui-review: no big outlined button per row), a 44px
 * target the same way every other tappable control on this screen is one.
 *
 * An older-format row (is_current_format false) gets the "older version"
 * label in place of anything restore-shaped - there is no restore
 * affordance anywhere in this card yet, current-format or not; #326 is what
 * adds one, and only ever for a current-format row.
 */
function AutoBackupList() {
  const [listState, listRun] = useApi<StoredBackup[]>()
  const [downloadError, setDownloadError] = useState<ApiError | undefined>(undefined)
  const [downloadingName, setDownloadingName] = useState<string | null>(null)

  useEffect(() => {
    void listRun(listStoredBackups)
  }, [listRun])

  async function handleDownload(name: string) {
    setDownloadError(undefined)
    setDownloadingName(name)
    try {
      await downloadStoredBackup(name)
    } catch (err) {
      setDownloadError(err instanceof ApiError ? err : new ApiError('unknown_error', err instanceof Error ? err.message : String(err)))
    } finally {
      setDownloadingName(null)
    }
  }

  return (
    <div className="flex flex-col gap-2 border-t border-border pt-3">
      <div className="flex flex-col gap-1">
        <h3 className="text-sm font-semibold">{text.autoHeading}</h3>
        <p className="text-sm text-muted-foreground">{text.autoBody}</p>
      </div>

      {listState.status === 'idle' || listState.status === 'loading' ? (
        <Loading />
      ) : listState.status === 'error' || !listState.data ? (
        listState.error && <ErrorState error={listState.error} onRetry={() => void listRun(listStoredBackups)} />
      ) : listState.data.length === 0 ? (
        <p className="text-sm text-muted-foreground">{text.empty}</p>
      ) : (
        <ul className="flex flex-col gap-2">
          {listState.data.map((item) => (
            <li key={item.name} className="flex items-center justify-between gap-3 rounded-lg bg-card px-4 py-2 ring-1 ring-foreground/10">
              <div className="flex min-w-0 flex-col gap-0.5">
                <span className="flex flex-wrap items-baseline gap-x-2">
                  <span className="font-medium">{formatIsoDate(item.date)}</span>
                  <span className="text-sm text-muted-foreground">{kindLabel(item.kind)}</span>
                </span>
                <span className="text-sm text-muted-foreground">
                  {formatBytes(item.size_bytes)}
                  {/* A hyphen, not a middle dot - CLAUDE.md rule 10: source
                      files are ASCII outside copy/id.ts. */}
                  {!item.is_current_format && ` - ${text.olderFormat}`}
                </span>
              </div>
              <button
                type="button"
                aria-label={text.downloadRowAria(kindLabel(item.kind), formatIsoDate(item.date))}
                disabled={downloadingName === item.name}
                onClick={() => void handleDownload(item.name)}
                className="flex size-11 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted/40 hover:text-foreground disabled:opacity-50"
              >
                <Download aria-hidden="true" className="size-4" />
              </button>
            </li>
          ))}
        </ul>
      )}

      {downloadError && <ErrorState error={downloadError} />}
    </div>
  )
}
