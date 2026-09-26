// Typed calls over apiFetch for the receipt-photo routes (M6.20 backend,
// M6.21/#154 frontend): an optional image attached to a transaction or a
// reimbursement claim, at record time or after the fact (ADR-011, PRD
// section 7.4).

import { apiFetch } from '@/lib/api'

/** The two kinds of row a receipt can attach to - also the routes'
 * `{parent}/{id}/receipts` segment, so a caller never has to spell the
 * path itself. */
export type ReceiptParentKind = 'transactions' | 'reimbursements'

/** POST .../receipts's 201 body (internal/http/receipts.go's
 * receiptResponse). The stored file path is never exposed - GET
 * /api/receipts/{id} is the only way to read the bytes back. */
export interface ReceiptUploadResult {
  id: number
  uploaded_at: number
}

/** GET /api/receipts/{id}'s URL - a same-origin, session-gated image
 * (internal/http/receipts.go's getReceipt), safe to use directly as an
 * <img src>. */
export function receiptUrl(id: number): string {
  return `/api/receipts/${id}`
}

/**
 * POST /api/{transactions|reimbursements}/{id}/receipts - multipart, one
 * "file" field. Always a second request after the parent row already
 * exists (the upload route needs its id), so a caller posts the parent
 * first and only then calls this - never rolling the parent back if this
 * one fails (see RecordTransaction.tsx's own comment on that).
 *
 * No Content-Type header set by hand: FormData sets its own multipart
 * boundary, and overriding it here would drop the boundary parameter and
 * break the server's parse.
 */
export function uploadReceipt(kind: ReceiptParentKind, parentId: number, file: File): Promise<ReceiptUploadResult> {
  const form = new FormData()
  form.append('file', file)
  return apiFetch<ReceiptUploadResult>(`/api/${kind}/${parentId}/receipts`, {
    method: 'POST',
    body: form,
  })
}

/** DELETE /api/receipts/{id} - for a wrong or duplicate photo. "Ganti foto"
 * (ReceiptDialog.tsx) is this followed by a fresh uploadReceipt - there is
 * no combined replace route. */
export function deleteReceipt(id: number): Promise<void> {
  return apiFetch<void>(`/api/receipts/${id}`, { method: 'DELETE' })
}
