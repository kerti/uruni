// The Cadangan card's one call (M6.37, #323, ADR-012): GET /api/backup,
// which answers with the whole fund as one zip rather than JSON - so this
// does not go through apiFetch (lib/api.ts), which always decodes a JSON
// body. The failure shape is kept identical to apiFetch's own (the same
// error envelope, the same ApiError) so a caller can render it through the
// app's one ErrorState component exactly like every other screen.

import { ApiError, NETWORK_ERROR_CODE } from '@/lib/api'
import { todayISODate } from '@/lib/dates'

interface ErrorEnvelope {
  error?: {
    code?: string
    message?: string
  }
}

/**
 * Downloads the backup zip and hands it to the browser to save as
 * `uruni-{today}.zip`, dated with her own local date (todayISODate) so a
 * second download never lands as "uruni(1).zip" beside the first. Named
 * here rather than from the server's Content-Disposition: the server's
 * clock may run in UTC, a day behind WIB before 07:00.
 */
export async function downloadBackup(): Promise<void> {
  let res: Response
  try {
    res = await fetch('/api/backup')
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

  const blob = await res.blob()
  saveBlob(blob, `uruni-${todayISODate()}.zip`)
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
