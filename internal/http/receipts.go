package http

// Receipt photos (PRD section 7.4, ADR-011): an optional image attached to a
// transaction or a reimbursement claim after the fact. Two upload routes
// rather than one that accepts either kind of id, matching the receipt
// table's own "exactly one parent" CHECK (transaction_id XOR
// reimbursement_id) - a route that could name either would have to re-derive
// that exclusivity itself instead of leaning on the schema for it.

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

	"github.com/kerti/uruni/internal/store"
)

// maxReceiptRequestBytes is the maintainer's own size cap (#153): 10 MB of
// raw request body, enforced with http.MaxBytesReader before a single byte of
// the multipart form is parsed - a phone photo well under this limit still
// downscales to a few hundred KB on disk (receipt_image.go), so the cap is
// about refusing something absurd early, not about the final stored size.
const maxReceiptRequestBytes = 10 << 20 // 10 MiB

// receiptResponse is the wire shape of an uploaded receipt: just enough for
// a client to list, link to (GET /api/receipts/{id}) and offer to delete.
// The on-disk path is never exposed - it is server-generated and has no
// meaning to a caller (#153's filename ruling, below).
type receiptResponse struct {
	ID         int64 `json:"id"`
	UploadedAt int64 `json:"uploaded_at"`
}

func toReceiptResponse(r store.Receipt) receiptResponse {
	return receiptResponse{ID: r.ID, UploadedAt: r.UploadedAt}
}

// uploadTransactionReceipt is POST /api/transactions/{id}/receipts: attaching
// a photo to an already-posted transaction. Nothing about the transaction
// row changes - ADR-011's whole point is that a receipt lives in its own
// table precisely so a photo can be added after the fact without touching an
// immutable ledger row.
func (a *api) uploadTransactionReceipt(w http.ResponseWriter, r *http.Request) {
	txn, ok := a.resolveTransactionForReceipt(w, r)
	if !ok {
		return
	}
	a.processAndStoreReceipt(w, r, txn.FundID, &txn.ID, nil)
}

// uploadReimbursementReceipt is POST /api/reimbursements/{id}/receipts: the
// same attachment, hung off a claim instead - PRD section 7.4's member
// fronting their own money, and the nota that proves it.
func (a *api) uploadReimbursementReceipt(w http.ResponseWriter, r *http.Request) {
	claim, ok := a.resolveReimbursementForReceipt(w, r)
	if !ok {
		return
	}
	a.processAndStoreReceipt(w, r, claim.FundID, nil, &claim.ID)
}

// resolveTransactionForReceipt reads {id}, fund-scopes it through
// GetTransactionForFund, and answers 404 for one belonging to another fund
// or naming no row at all - the exact shape resolveDuesTier and resolveAccount
// already use for every other route that reaches a row by path id.
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
		mapSQLiteError(w, a.logger, err) // sql.ErrNoRows -> 404 not_found
		return store.Transaction{}, false
	}
	return txn, true
}

// resolveReimbursementForReceipt is resolveTransactionForReceipt's twin over
// reimbursement, using the same fund-scoped GetReimbursement every other
// reimbursement route already reads through.
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
		mapSQLiteError(w, a.logger, err)
		return store.Reimbursement{}, false
	}
	return claim, true
}

// resolveReceipt is GET and DELETE's shared lookup: {id}, fund-scoped through
// GetReceiptForFund. This is the one gate between a session and someone
// else's photo - GET answers image bytes, not JSON, so there is no second
// layer downstream that would otherwise catch a wrong fund's row.
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
		mapSQLiteError(w, a.logger, err)
		return store.Receipt{}, false
	}
	return receipt, true
}

// processAndStoreReceipt is the two upload routes' shared body: read the
// multipart file (capped, per maxReceiptRequestBytes), downscale and
// re-encode it (receipt_image.go), write it to the uploads volume under a
// server-generated name, and insert the row. Exactly one of transactionID
// and reimbursementID is non-nil, matching the schema's own CHECK - the two
// callers above are what enforce that, not this function.
func (a *api) processAndStoreReceipt(w http.ResponseWriter, r *http.Request, fundID int64, transactionID, reimbursementID *int64) {
	raw, ok := a.readReceiptUpload(w, r)
	if !ok {
		return
	}

	processed, err := processReceiptImage(raw)
	if err != nil {
		switch {
		case errors.Is(err, errReceiptHEICUnsupported):
			// Distinct from the generic rejection below on purpose (the
			// maintainer's own ruling, #153): a phone shooting HEIC by
			// default is the single most likely reason this path is ever
			// reached at all, and the fix (re-export or share as JPEG) is
			// different from "pick a different kind of file."
			writeAPIError(w, http.StatusUnsupportedMediaType, "heic_unsupported",
				"HEIC/HEIF photos are not supported here - share or export the photo as JPEG or PNG instead.")
		case errors.Is(err, errReceiptTooManyPixels):
			writeAPIError(w, http.StatusRequestEntityTooLarge, "image_too_large",
				"The photo's dimensions are too large. Use a photo of 50 megapixels or less.")
		case errors.Is(err, errReceiptUnsupportedMediaType):
			writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media_type",
				"That file is not a supported image type. Use JPEG, PNG or WebP.")
		default:
			a.logger.Error("processing receipt image", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		}
		return
	}

	filename, err := writeReceiptFile(a.uploadsDir, processed)
	if err != nil {
		a.logger.Error("writing receipt file", "error", err)
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
		// The file now sits on disk with no row naming it - unlike the
		// orphan ADR-011 already accepts from a *delete* (reachable, until
		// then, through the id that named it), this one never had an id at
		// all, so best-effort cleanup here is the only chance to remove it.
		_ = os.Remove(filepath.Join(a.uploadsDir, filename))
		mapSQLiteError(w, a.logger, err)
		return
	}

	writeJSON(w, http.StatusCreated, toReceiptResponse(receipt))
}

// readReceiptUpload enforces the size cap and pulls the uploaded file's raw
// bytes out of the multipart body - the "file" field, by this route's own
// contract. It answers the request itself on any failure, the same
// decode-and-report shape decodeJSON uses for a JSON body.
func (a *api) readReceiptUpload(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	// Wraps r.Body before ParseMultipartForm ever reads a byte of it - the
	// cap has to apply to the whole raw request body (maintainer's own
	// wording), not to some part parsed out of it afterward.
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
// to the uploads directory - never derived from anything the client sent, so
// there is no path-traversal surface here to defend). It writes to a temp
// file in the same directory first and renames into place, so a reader can
// never observe a partially-written file under the final name.
func writeReceiptFile(uploadsDir string, data []byte) (string, error) {
	name, err := randomReceiptFilename()
	if err != nil {
		return "", err
	}

	// In uploadsDir itself, not os.TempDir(): os.Rename across filesystems
	// fails, and a container's /uploads volume is very often a different
	// filesystem from wherever the OS default temp directory lands.
	tmp, err := os.CreateTemp(uploadsDir, ".receipt-upload-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	if err := os.Rename(tmpPath, filepath.Join(uploadsDir, name)); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return name, nil
}

// randomReceiptFilename returns a fresh crypto/rand name, never derived from
// the client's own filename - the maintainer's own ruling (#153): nothing
// about a stored receipt's name is ever attacker-influenced, so there is no
// path-traversal surface to sanitize against in the first place. Every
// receipt is re-encoded as JPEG (receipt_image.go), so the extension is
// always .jpg regardless of what was uploaded.
func randomReceiptFilename() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf) + ".jpg", nil
}

// receiptIDsForTransactions batch-loads receipt_ids for a fund-scoped page of
// transaction rows (#154), one query per page rather than one per row
// (N+1). The returned map holds an entry only for a transaction id that
// actually has at least one receipt attached - toTransactionResponse already
// defaults every row's ReceiptIDs to []int64{}, so a caller only needs to
// overwrite the ids this map actually names, via orEmptyReceiptIDs below.
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

// receiptIDsForReimbursements is receiptIDsForTransactions' twin over
// reimbursement claims, for GET /api/reimbursements's page and for
// PATCH /api/reimbursements/{id}'s single-row response (a claim corrected
// after a photo was already attached to it must not report an empty
// receipt_ids just because this response is not, itself, the create route).
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

// orEmptyReceiptIDs turns receiptIDsForTransactions/receiptIDsForReimbursements'
// "absent means none" map lookup into receipt_ids' own "always [], never
// null" wire contract (the maintainer's ruling on #154).
func orEmptyReceiptIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}

// getReceipt is GET /api/receipts/{id}: the first route in this whole
// surface that answers with image bytes instead of the JSON envelope every
// other route shares. Session-gated rather than public (#153's own ruling) -
// see api.go's route comment for why.
func (a *api) getReceipt(w http.ResponseWriter, r *http.Request) {
	receipt, ok := a.resolveReceipt(w, r)
	if !ok {
		return
	}

	// Defense in depth, not a check this path can actually fail today:
	// receipt.Path is always the bare filename writeReceiptFile generated,
	// with no directory separators to begin with. Refusing anything else
	// here is what keeps that true rather than merely documented, the
	// moment something upstream changes.
	if receipt.Path != filepath.Base(receipt.Path) {
		a.logger.Error("receipt path is not a bare filename", "id", receipt.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}

	file, err := os.Open(filepath.Join(a.uploadsDir, receipt.Path))
	if err != nil {
		a.logger.Error("opening receipt file", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	defer func() { _ = file.Close() }()

	// Every receipt is stored as JPEG (receipt_image.go always re-encodes),
	// so the type is a known constant, not something to sniff or trust from
	// the stored row.
	w.Header().Set("Content-Type", "image/jpeg")
	// The browser must not guess past the Content-Type above - this is a
	// photo a treasurer uploaded, not a document to render as anything else.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// private: this is one treasurer's own session, never a shared cache
	// sitting in front of the app. A day is long enough that flipping
	// between receipts in one sitting does not re-fetch each one, short
	// enough that a deleted receipt's bytes do not linger indefinitely in a
	// phone's cache under an id nothing serves any more.
	w.Header().Set("Cache-Control", "private, max-age=86400")

	http.ServeContent(w, r, receipt.Path, time.Unix(receipt.UploadedAt, 0), file)
}

// deleteReceipt is DELETE /api/receipts/{id}: for a wrong or duplicate photo
// (ADR-011: "a wrong photo is replaceable"). It removes the database row and
// then best-effort removes the file - ADR-011 already accepts an orphaned
// file as the cost of not making image deletion transactional ("a deleted
// receipt row leaves an orphaned file on the volume until something sweeps
// it"), so a failed os.Remove here is neither reported to the caller nor
// retried; there is deliberately no sweep job.
func (a *api) deleteReceipt(w http.ResponseWriter, r *http.Request) {
	receipt, ok := a.resolveReceipt(w, r)
	if !ok {
		return
	}

	if err := a.queries.DeleteReceipt(r.Context(), receipt.ID); err != nil {
		mapSQLiteDeleteError(w, a.logger, err)
		return
	}

	_ = os.Remove(filepath.Join(a.uploadsDir, receipt.Path))

	w.WriteHeader(http.StatusNoContent)
}
