// Typed calls over apiFetch for restoring from an uploaded backup (M6.39,
// #325, ADR-012): POST /api/restore/inspect (multipart, answers with a
// preview and a short-lived token) and POST /api/restore/confirm (the
// token plus the treasurer's current password - the only call that
// actually changes anything). inspectStoredRestore (M6.40, #326) is the
// same first step for one of the server's own stored dumps: no file, no
// multipart body - just the name GET /api/backups already listed - and it
// answers with the identical RestoreInspectResult shape, so confirmRestore
// below never needs to know which of the two inspect calls staged what it
// is about to confirm.

import { apiFetch } from '@/lib/api'

/** A file fund's own place in the restore preview - kept (present on both
 * sides), removed (live only) or added (file only). Mirrors
 * internal/backup's FundStatus exactly. */
export type FundRestoreStatus = 'kept' | 'removed' | 'added'

/** One line of the confirm step's per-fund list (internal/backup's
 * FundPreview). transactions_lost and cutoff_date are only meaningful when
 * status is "kept". */
export interface FundRestorePreview {
  fund_id: number
  name: string
  status: FundRestoreStatus
  transactions_lost: number
  cutoff_date?: string
}

/** POST /api/restore/inspect's own preview block (internal/backup's
 * Preview): what the confirm step shows before anything changes. */
export interface RestorePreview {
  date: string
  total: number
  funds: FundRestorePreview[]
}

/** POST /api/restore/inspect's whole body. */
export interface RestoreInspectResult {
  token: string
  preview: RestorePreview
}

/**
 * POST /api/restore/inspect - multipart, one "file" field, the same shape
 * receipts.ts's own uploadReceipt uses. Decodes and validates the upload
 * server-side but changes nothing; the token it returns is what
 * confirmRestore below must send back, so the (possibly large) file itself
 * is never uploaded a second time.
 */
export function inspectRestoreUpload(file: File): Promise<RestoreInspectResult> {
  const form = new FormData()
  form.append('file', file)
  return apiFetch<RestoreInspectResult>('/api/restore/inspect', {
    method: 'POST',
    body: form,
  })
}

/**
 * POST /api/restore/inspect-stored/{name} - the stored-backup twin of
 * inspectRestoreUpload above: name is a StoredBackup's own `name` (lib/
 * backup.ts), the exact server-side identifier GET /api/backups already
 * handed the client, never a path. Changes nothing server-side either -
 * an older-format dump is refused here the same way an older-format upload
 * would be (internal/backup's ParseUpload, the one place that check lives).
 */
export function inspectStoredRestore(name: string): Promise<RestoreInspectResult> {
  return apiFetch<RestoreInspectResult>(`/api/restore/inspect-stored/${encodeURIComponent(name)}`, {
    method: 'POST',
  })
}

/**
 * POST /api/restore/confirm - the one call that restores anything. A
 * success means every session is gone, including this one's own - the
 * caller's job afterward is to send the treasurer back to a fresh login,
 * not to keep using this session for anything else.
 */
export function confirmRestore(token: string, password: string): Promise<void> {
  return apiFetch<void>('/api/restore/confirm', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token, password }),
  })
}
