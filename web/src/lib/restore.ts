// Typed calls over apiFetch for restoring from a backup (ADR-012):
// POST /api/restore/inspect (multipart; answers with a preview and a
// short-lived token) and POST /api/restore/confirm (the token plus the
// treasurer's current password - the only call that changes anything).
// inspectStoredRestore is the same first step for one of the server's stored
// dumps and answers with the identical RestoreInspectResult, so confirmRestore
// never needs to know which inspect call staged what it confirms.

import { apiFetch, plainConnectionError } from '@/lib/api'

/** A file fund's place in the restore preview - kept (present on both sides),
 * removed (live only) or added (file only). Mirrors internal/backup's
 * FundStatus. */
export type FundRestoreStatus = 'kept' | 'removed' | 'added'

/** One line of the confirm step's per-fund list (internal/backup's
 * FundPreview). transactions_lost and cutoff_date only mean something when
 * status is "kept". */
export interface FundRestorePreview {
  fund_id: number
  name: string
  status: FundRestoreStatus
  transactions_lost: number
  cutoff_date?: string
}

/** POST /api/restore/inspect's preview block (internal/backup's Preview). */
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
 * POST /api/restore/inspect - multipart, one "file" field. Decodes and
 * validates the upload server-side but changes nothing; the token it returns
 * is what confirmRestore sends back, so the (possibly large) file is never
 * uploaded twice.
 */
export function inspectRestoreUpload(file: File): Promise<RestoreInspectResult> {
  const form = new FormData()
  form.append('file', file)
  return apiFetch<RestoreInspectResult>('/api/restore/inspect', {
    method: 'POST',
    body: form,
  }).catch(plainConnectionError)
}

/**
 * POST /api/restore/inspect-stored/{name} - the stored-backup twin of
 * inspectRestoreUpload: name is a StoredBackup's `name` (lib/backup.ts), the
 * server-side identifier GET /api/backups handed the client, never a path.
 * Changes nothing either - an older-format dump is refused here as an
 * older-format upload would be (internal/backup's ParseUpload, the one place
 * that check lives).
 */
export function inspectStoredRestore(name: string): Promise<RestoreInspectResult> {
  return apiFetch<RestoreInspectResult>(`/api/restore/inspect-stored/${encodeURIComponent(name)}`, {
    method: 'POST',
  }).catch(plainConnectionError)
}

/**
 * POST /api/restore/confirm - the one call that restores anything. A success
 * means every session is gone, including this one's - the caller sends the
 * treasurer back to a fresh login.
 */
export function confirmRestore(token: string, password: string): Promise<void> {
  return apiFetch<void>('/api/restore/confirm', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token, password }),
  })
}
