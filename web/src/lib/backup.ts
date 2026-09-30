// The Cadangan card's calls (M6.37 #323, M6.38 #324, ADR-012/013):
// GET /api/backup (an on-demand zip, built fresh every call), GET
// /api/backups (the list of server-side dumps ADR-013's scheduler already
// wrote) and GET /api/backups/{name} (downloading one of them). The two zip
// downloads answer with a zip rather than JSON, so neither goes through
// apiFetch (lib/api.ts), which always decodes a JSON body - fetchZip below
// is their shared plumbing, with the same error envelope and the same
// ApiError apiFetch itself throws, so a caller renders either failure
// through the app's one ErrorState component exactly like every other
// screen. The list call is plain JSON, so it is a plain apiFetch.

import { ApiError, NETWORK_ERROR_CODE, apiFetch } from '@/lib/api'
import { todayISODate } from '@/lib/dates'

interface ErrorEnvelope {
  error?: {
    code?: string
    message?: string
  }
}

/** One dump GET /api/backups lists (internal/http/backup.go's
 * backupListItem) - date and format_version are the server's own read of
 * the dump's filename, in Asia/Jakarta; the card never parses a name
 * itself. */
export interface StoredBackup {
  name: string
  date: string // YYYY-MM-DD, Asia/Jakarta
  kind: 'daily' | 'pre-restore'
  format_version: number
  is_current_format: boolean
  size_bytes: number
}

/** GET /api/backups - every server-side dump, newest first, empty array
 * for a fresh instance with none yet. */
export function listStoredBackups(): Promise<StoredBackup[]> {
  return apiFetch<StoredBackup[]>('/api/backups')
}

/**
 * Downloads the backup zip and hands it to the browser to save as
 * `uruni-{today}.zip`, dated with her own local date (todayISODate) so a
 * second download never lands as "uruni(1).zip" beside the first. Named
 * here rather than from the server's Content-Disposition: the server's
 * clock may run in UTC, a day behind WIB before 07:00.
 */
export async function downloadBackup(): Promise<void> {
  const blob = await fetchZip('/api/backup')
  saveBlob(blob, `uruni-${todayISODate()}.zip`)
}

/**
 * Downloads one already-written server-side dump (a StoredBackup's own
 * `name`) and saves it under that exact name - unlike downloadBackup above,
 * there is no local date to prefer: the name already carries the
 * treasurer's own WIB date (internal/backup/dumps.go's BuildDumpName), and
 * saving under a different name than what the card shows would be its own
 * small confusion.
 */
export async function downloadStoredBackup(name: string): Promise<void> {
  const blob = await fetchZip(`/api/backups/${encodeURIComponent(name)}`)
  saveBlob(blob, name)
}

async function fetchZip(url: string): Promise<Blob> {
  let res: Response
  try {
    res = await fetch(url)
  } catch (err) {
    throw new ApiError(NETWORK_ERROR_CODE, err instanceof Error ? err.message : 'network error', true)
  }

  if (!res.ok) {
    let envelope: ErrorEnvelope = {}
    try {
      envelope = (await res.json()) as ErrorEnvelope
    } catch {
      // Body wasn't JSON (or was empty) - fall through to the generic code.
    }
    const code = envelope.error?.code ?? 'unknown_error'
    const message = envelope.error?.message ?? `Request failed with status ${res.status}`
    throw new ApiError(code, message)
  }

  return res.blob()
}

function saveBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
}
