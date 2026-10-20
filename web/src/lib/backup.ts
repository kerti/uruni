// The Cadangan card's calls (ADR-012/013): GET /api/backup (an on-demand zip,
// built fresh every call), GET /api/backups (the server-side dumps ADR-013's
// scheduler wrote) and GET /api/backups/{name} (downloading one). The two zip
// downloads answer with a zip, not JSON, so they go through fetchBlob
// (lib/api.ts), which throws the same ApiError apiFetch does. The list call
// is plain JSON, so a plain apiFetch.

import { apiFetch, fetchBlob, saveBlob } from '@/lib/api'
import { todayISODate } from '@/lib/dates'

/** One dump GET /api/backups lists (backupListItem) - date and format_version
 * are the server's read of the dump's filename, in Asia/Jakarta; the card
 * never parses a name itself. */
export interface StoredBackup {
  name: string
  date: string // YYYY-MM-DD, Asia/Jakarta
  kind: 'daily' | 'pre-restore'
  format_version: number
  /** Written in the format this build writes. An older one is labelled. */
  is_current_format: boolean
  /** The importer can read it - the current format, or an older one it upgrades
   * (ADR-040) - so the restore control follows this, not is_current_format. */
  can_restore: boolean
  size_bytes: number
}

/** GET /api/backups - every server-side dump, newest first. */
export function listStoredBackups(): Promise<StoredBackup[]> {
  return apiFetch<StoredBackup[]>('/api/backups')
}

/**
 * Downloads the backup zip and hands it to the browser to save as
 * `uruni-{today}.zip`, dated with her local date (todayISODate) so a second
 * download never lands as "uruni(1).zip". Named here rather than from the
 * server's Content-Disposition: the server's clock may run in UTC, a day
 * behind WIB before 07:00.
 */
export async function downloadBackup(): Promise<void> {
  const { blob } = await fetchBlob('/api/backup')
  saveBlob(blob, `uruni-${todayISODate()}.zip`)
}

/**
 * Downloads one already-written server-side dump (a StoredBackup's `name`)
 * and saves it under that exact name: it already carries the treasurer's WIB
 * date (internal/backup/dumps.go's BuildDumpName), and a different name than
 * the card shows would confuse.
 */
export async function downloadStoredBackup(name: string): Promise<void> {
  const { blob } = await fetchBlob(`/api/backups/${encodeURIComponent(name)}`)
  saveBlob(blob, name)
}
