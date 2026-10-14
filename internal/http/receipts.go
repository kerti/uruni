package http

// Receipt photos (PRD section 7.4, ADR-011): an optional image attached to a
// transaction or reimbursement claim after the fact. Two upload routes, not
// one accepting either id, match the receipt table's "exactly one parent"
// CHECK (transaction_id XOR reimbursement_id) without re-deriving that
// exclusivity.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/store"
)

// maxReceiptRequestBytes caps the raw request body at 10 MB (#153), enforced
// with http.MaxBytesReader before any of the multipart form is parsed. A
// phone photo under it downscales to a few hundred KB (receipt_image.go), so
// the cap refuses something absurd early; it is not the stored size.
const maxReceiptRequestBytes = 10 << 20 // 10 MiB

// receiptResponse is the wire shape of an uploaded receipt: enough for a
// client to list, link to (GET /api/receipts/{id}) and offer to delete. The
// on-disk path is never exposed: it is server-generated and meaningless to a
// caller.
type receiptResponse struct {
	ID         int64 `json:"id"`
	UploadedAt int64 `json:"uploaded_at"`
}

func toReceiptResponse(r store.Receipt) receiptResponse {
	return receiptResponse{ID: r.ID, UploadedAt: r.UploadedAt}
}

// uploadTransactionReceipt is POST /api/transactions/{id}/receipts: attaching
// a photo to a posted transaction. The row does not change: ADR-011 puts a
// receipt in its own table so a photo can be added without touching an
// immutable ledger row.
func (a *api) uploadTransactionReceipt(w http.ResponseWriter, r *http.Request) {
	txn, ok := a.resolveTransactionForReceipt(w, r)
	if !ok {
		return
	}
	a.processAndStoreReceipt(w, r, txn.FundID, &txn.ID, nil)
}

// uploadReimbursementReceipt is POST /api/reimbursements/{id}/receipts: the
// same attachment on a claim (PRD section 7.4: a member fronting money, and
// the nota that proves it).
func (a *api) uploadReimbursementReceipt(w http.ResponseWriter, r *http.Request) {
	claim, ok := a.resolveReimbursementForReceipt(w, r)
	if !ok {
		return
	}
	a.processAndStoreReceipt(w, r, claim.FundID, nil, &claim.ID)
}

// resolveTransactionForReceipt reads {id}, fund-scopes it through
// GetTransactionForFund, and answers 404 for another fund's row or none, as
// resolveDuesTier and resolveAccount do.
func (a *api) resolveTransactionForReceipt(w http.ResponseWriter, r *http.Request) (store.Transaction, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The transaction id is not a valid number.")
		return store.Transaction{}, false
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return store.Transaction{}, false
	}

	txn, err := a.queries.GetTransactionForFund(r.Context(), store.GetTransactionForFundParams{FundID: fund.ID, ID: id})
	if err != nil {
		mapSQLiteError(w, a.log(r), err) // sql.ErrNoRows -> 404 not_found
		return store.Transaction{}, false
	}
	return txn, true
}

// resolveReimbursementForReceipt is resolveTransactionForReceipt's twin over
// reimbursement, through the fund-scoped GetReimbursement.
func (a *api) resolveReimbursementForReceipt(w http.ResponseWriter, r *http.Request) (store.Reimbursement, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The reimbursement id is not a valid number.")
		return store.Reimbursement{}, false
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return store.Reimbursement{}, false
	}

	claim, err := a.queries.GetReimbursement(r.Context(), store.GetReimbursementParams{ID: id, FundID: fund.ID})
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return store.Reimbursement{}, false
	}
	return claim, true
}

// resolveReceipt is GET and DELETE's shared lookup: {id}, fund-scoped through
// GetReceiptForFund. It is the one gate between a session and someone else's
// photo: GET answers image bytes, so no downstream layer would catch a wrong
// fund's row.
func (a *api) resolveReceipt(w http.ResponseWriter, r *http.Request) (store.Receipt, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The receipt id is not a valid number.")
		return store.Receipt{}, false
	}

	fund, ok := a.resolveFund(w, r)
	if !ok {
		return store.Receipt{}, false
	}

	receipt, err := a.queries.GetReceiptForFund(r.Context(), store.GetReceiptForFundParams{ID: id, FundID: fund.ID})
	if err != nil {
		mapSQLiteError(w, a.log(r), err)
		return store.Receipt{}, false
	}
	return receipt, true
}

// processAndStoreReceipt is the two upload routes' shared body: read the
// multipart file (capped by maxReceiptRequestBytes), downscale and re-encode
// it (receipt_image.go), write it to the uploads volume under a
// server-generated name, and insert the row. Exactly one of transactionID and
// reimbursementID is non-nil (the schema's CHECK); the two callers enforce
// that.
func (a *api) processAndStoreReceipt(w http.ResponseWriter, r *http.Request, fundID int64, transactionID, reimbursementID *int64) {
	raw, ok := a.readReceiptUpload(w, r)
	if !ok {
		return
	}

	processed, err := processReceiptImage(raw)
	if err != nil {
		switch {
		case errors.Is(err, errReceiptHEICUnsupported):
			// Distinct from the generic rejection below (#153): a phone shooting HEIC by
			// default is the likeliest way to reach this, and the fix (re-export or share
			// as JPEG) differs from "pick a different kind of file."
			writeAPIError(w, http.StatusUnsupportedMediaType, "heic_unsupported",
				"HEIC/HEIF photos are not supported here - share or export the photo as JPEG or PNG instead.")
		case errors.Is(err, errReceiptTooManyPixels):
			writeAPIError(w, http.StatusRequestEntityTooLarge, "image_too_large",
				"The photo's dimensions are too large. Use a photo of 50 megapixels or less.")
		case errors.Is(err, errReceiptUnsupportedMediaType):
			writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
				"That file is not a supported image type. Use JPEG, PNG or WebP.")
		default:
			a.log(r).Error("processing receipt image", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		}
		return
	}

	filename, err := writeReceiptFile(a.uploadsDir, processed)
	if err != nil {
		a.log(r).Error("writing receipt file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	receipt, err := a.queries.CreateReceipt(r.Context(), store.CreateReceiptParams{
		FundID:          fundID,
		TransactionID:   transactionID,
		ReimbursementID: reimbursementID,
		Path:            filename,
		UploadedAt:      time.Now().Unix(),
	})
	if err != nil {
		// The file is on disk with no row naming it. Unlike the orphan ADR-011
		// accepts from a delete (reachable through its id until then), this one never
		// had an id, so best-effort cleanup here is the only chance to remove it.
		_ = os.Remove(filepath.Join(a.uploadsDir, filename))
		mapSQLiteError(w, a.log(r), err)
		return
	}

	writeJSON(w, http.StatusCreated, toReceiptResponse(receipt))
}

// readReceiptUpload enforces the size cap and pulls the "file" field's raw
// bytes out of the multipart body. It answers the request itself on failure,
// as decodeJSON does for JSON.
func (a *api) readReceiptUpload(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	// Wraps r.Body before ParseMultipartForm reads any of it: the cap applies to
	// the whole raw body, not a part parsed from it.
	r.Body = http.MaxBytesReader(w, r.Body, maxReceiptRequestBytes)

	if err := r.ParseMultipartForm(maxReceiptRequestBytes); err != nil { //nolint:gosec // not unbounded - r.Body is already wrapped in http.MaxBytesReader above, which is exactly what caps this read
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "payload_too_large",
				"The receipt file is larger than the 10 MB limit.")
			return nil, false
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The request is not a valid file upload.")
		return nil, false
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, _, err := r.FormFile("file")
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "A receipt file is required.")
		return nil, false
	}
	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(file)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The receipt file could not be read.")
		return nil, false
	}
	return raw, true
}

// writeReceiptFile writes data under a fresh, server-generated name inside
// uploadsDir and returns that name (the path CreateReceipt stores, relative
// to uploadsDir, never derived from client input, so no path-traversal
// surface). It writes a temp file in the same directory and renames into
// place, so a reader never sees a partial file under the final name.
func writeReceiptFile(uploadsDir string, data []byte) (string, error) {
	name, err := randomReceiptFilename()
	if err != nil {
		return "", err
	}

	// In uploadsDir, not os.TempDir(): os.Rename across filesystems fails, and a
	// container's /uploads volume is often a different filesystem from the OS
	// temp directory. WriteFileAtomic also fsyncs the file before the rename and
	// the directory after, so a power loss cannot leave a receipt row naming an
	// empty file (#501).
	err = backup.WriteFileAtomic(uploadsDir, ".receipt-upload-*.tmp", name, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
	if err != nil {
		return "", err
	}
	return name, nil
}

// randomReceiptFilename returns a fresh crypto/rand name, never derived from
// the client's filename (#153): nothing about a stored receipt's name is
// attacker-influenced, so there is no path-traversal surface to sanitize.
// Every receipt is re-encoded as JPEG (receipt_image.go), so the extension is
// always .jpg.
func randomReceiptFilename() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ".jpg", nil
}

// receiptIDsForTransactions batch-loads receipt_ids for a fund-scoped page of
// transaction rows (#154), one query per page, not per row. The map has an
// entry only for a transaction with a receipt; toTransactionResponse defaults
// ReceiptIDs to []int64{}, so callers overwrite only the named ids via
// orEmptyReceiptIDs.
func (a *api) receiptIDsForTransactions(ctx context.Context, fundID int64, ids []int64) (map[int64][]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	sliceIDs := make([]*int64, len(ids))
	for i := range ids {
		sliceIDs[i] = &ids[i]
	}
	rows, err := a.queries.ListReceiptIDsByTransactionIDs(ctx, store.ListReceiptIDsByTransactionIDsParams{
		FundID:         fundID,
		TransactionIds: sliceIDs,
	})
	if err != nil {
		return nil, err
	}
	byParent := make(map[int64][]int64, len(rows))
	for _, row := range rows {
		if row.TransactionID == nil {
			continue // can't happen: the query's own WHERE already excludes a NULL transaction_id
		}
		byParent[*row.TransactionID] = append(byParent[*row.TransactionID], row.ID)
	}
	return byParent, nil
}

// receiptIDsForReimbursements is receiptIDsForTransactions' twin over claims,
// for GET /api/reimbursements's page and PATCH /api/reimbursements/{id}'s
// response (a claim corrected after a photo was attached must not report
// empty receipt_ids).
func (a *api) receiptIDsForReimbursements(ctx context.Context, fundID int64, ids []int64) (map[int64][]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	sliceIDs := make([]*int64, len(ids))
	for i := range ids {
		sliceIDs[i] = &ids[i]
	}
	rows, err := a.queries.ListReceiptIDsByReimbursementIDs(ctx, store.ListReceiptIDsByReimbursementIDsParams{
		FundID:           fundID,
		ReimbursementIds: sliceIDs,
	})
	if err != nil {
		return nil, err
	}
	byParent := make(map[int64][]int64, len(rows))
	for _, row := range rows {
		if row.ReimbursementID == nil {
			continue // can't happen: the query's own WHERE already excludes a NULL reimbursement_id
		}
		byParent[*row.ReimbursementID] = append(byParent[*row.ReimbursementID], row.ID)
	}
	return byParent, nil
}

// orEmptyReceiptIDs turns the "absent means none" map lookup of
// receiptIDsForTransactions/receiptIDsForReimbursements into receipt_ids'
// "always [], never null" wire contract (#154).
func orEmptyReceiptIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

// getReceipt is GET /api/receipts/{id}: the first route answering with image
// bytes instead of the JSON envelope. Session-gated, not public (#153); see
// api.go.
func (a *api) getReceipt(w http.ResponseWriter, r *http.Request) {
	receipt, ok := a.resolveReceipt(w, r)
	if !ok {
		return
	}

	// Defense in depth, not a check this path can fail today: receipt.Path is the
	// bare filename writeReceiptFile generated. Refusing anything else keeps that
	// true if something upstream changes.
	if receipt.Path != filepath.Base(receipt.Path) {
		a.log(r).Error("receipt path is not a bare filename", "id", receipt.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	file, err := os.Open(filepath.Join(a.uploadsDir, receipt.Path))
	if err != nil {
		a.log(r).Error("opening receipt file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	defer func() { _ = file.Close() }()

	// Every receipt is stored as JPEG (receipt_image.go re-encodes), so the type
	// is a known constant, not sniffed or trusted from the row.
	w.Header().Set("Content-Type", "image/jpeg")
	// The browser must not guess past the Content-Type: this is an uploaded
	// photo, not a document to render as anything else.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// private: one treasurer's session, never a shared cache. no-cache, not
	// max-age: receipt ids are reused (SQLite hands a deleted top id to the next
	// insert), so "Ganti foto" on the newest receipt serves a new photo at the
	// old URL, and a cache trusted by URL alone kept showing the old one (#459).
	// The ETag is the stored file name, random per upload, so an unchanged photo
	// is a 304 and a replaced one never is.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", `"`+receipt.Path+`"`)

	http.ServeContent(w, r, receipt.Path, time.Unix(receipt.UploadedAt, 0), file)
}

// deleteReceipt is DELETE /api/receipts/{id}: for a wrong or duplicate photo
// (ADR-011: "a wrong photo is replaceable"). It removes the row, then
// best-effort removes the file: ADR-011 accepts an orphaned file as the cost
// of non-transactional image deletion, so a failed os.Remove is neither
// reported nor retried, and there is no sweep job.
func (a *api) deleteReceipt(w http.ResponseWriter, r *http.Request) {
	receipt, ok := a.resolveReceipt(w, r)
	if !ok {
		return
	}

	if err := a.queries.DeleteReceipt(r.Context(), receipt.ID); err != nil {
		mapSQLiteDeleteError(w, a.log(r), err)
		return
	}

	_ = os.Remove(filepath.Join(a.uploadsDir, receipt.Path))

	w.WriteHeader(http.StatusNoContent)
}
