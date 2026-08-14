package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
)

// errorEnvelope is the wire shape of every API error:
// {"error":{"code":"snake_case_slug","message":"An English sentence."}}.
//
// Both fields are English. The API is ADR-014's "code" surface — Indonesian
// never appears on the wire; the SPA maps code to the treasurer-facing copy.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeAPIError writes the error envelope. Every route in this package answers
// a failure through this one function, so the shape can't drift route by
// route.
func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

// mapLedgerError answers an error returned by an internal/ledger call, per
// ADR-027's sentinel taxonomy. Every later M4 slice that calls into the ledger
// shares this one switch rather than re-deriving the mapping at each route.
//
// ErrOpeningBalanceExists maps to 409 alongside the two reimbursement
// sentinels and ErrIncidentalAlreadyClosed even though ADR-027's own closing
// sentence lists only the latter three — its doc comment gives the identical
// reasoning (a pre-check ahead of a unique index the schema already enforces),
// and that ADR is `implemented`, not `draft`, so the omission is corrected here
// rather than by editing it.
func mapLedgerError(w http.ResponseWriter, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, ledger.ErrInvalidArgument):
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The request contains an invalid argument.")
	case errors.Is(err, ledger.ErrReimbursementWaived):
		writeAPIError(w, http.StatusConflict, "reimbursement_waived", "This reimbursement has been waived.")
	case errors.Is(err, ledger.ErrReimbursementAlreadySettled):
		writeAPIError(w, http.StatusConflict, "reimbursement_already_settled", "This reimbursement has already been settled.")
	case errors.Is(err, ledger.ErrIncidentalAlreadyClosed):
		writeAPIError(w, http.StatusConflict, "incidental_already_closed", "This incidental has already been closed.")
	case errors.Is(err, ledger.ErrOpeningBalanceExists):
		writeAPIError(w, http.StatusConflict, "opening_balance_exists", "An opening balance already exists for this account.")
	case errors.Is(err, ledger.ErrFundAlreadyExists):
		writeAPIError(w, http.StatusConflict, "fund_already_exists", "A fund has already been set up.")
	case errors.Is(err, money.ErrOverflow):
		// err's own message embeds the operands that overflowed — the amounts
		// themselves — which ADR-022 forbids logging. Every other unrecognized
		// error reaching the default case below wraps an id, not an amount
		// (ADR-027: "surfaces wrapped generically... for M4 to map to a 500"),
		// so only this one case needs to withhold the message.
		logger.Error("unhandled ledger error", "kind", "overflow")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
	default:
		logger.Error("unhandled ledger error", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
	}
}

// mapSQLiteError answers an error from a direct-CRUD route — one that calls
// store.Queries itself rather than internal/ledger, so none of ADR-027's
// sentinels apply. There is no existing taxonomy for this in the codebase; it
// reads the driver's own result code rather than the ADR-027 sentinels above.
//
// errors.As against *sqlite.Error and Code() against the modernc.org/sqlite/lib
// constants, never a string match against the driver's message: the message is
// not a stable contract across driver releases, the extended result code is.
func mapSQLiteError(w http.ResponseWriter, logger *slog.Logger, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
		return
	}

	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		switch sqliteErr.Code() {
		case sqlite3.SQLITE_CONSTRAINT_CHECK:
			writeAPIError(w, http.StatusBadRequest, "check_violation", "The request violates a data constraint.")
			return
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			writeAPIError(w, http.StatusConflict, "unique_violation", "This value conflicts with an existing record.")
			return
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			// A reference to a row that does not exist - e.g. a member's
			// tier_id naming no dues_tier. #63's raw-SQLite driver check
			// (internal/db's foreign_keys=ON) is what makes this reachable
			// at all; without it SQLite would silently accept the row. 400,
			// not 404: the failing id is a field on the request the client
			// sent, not a path segment naming "the resource" being fetched.
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The request references a record that does not exist.")
			return
		}
	}

	logger.Error("unhandled sqlite error", "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
}

// mapSQLiteDeleteError is mapSQLiteError's counterpart for a DELETE.
// SQLITE_CONSTRAINT_FOREIGNKEY carries two opposite meanings and one result
// code, so the calling handler is what distinguishes them: on a create or an
// update it means "you named a row that doesn't exist" (400, above); on a
// delete it means the row exists and real data still points at it - a member
// with posted transactions - which is a conflict, not a malformed request.
// Every other class means the same either way and delegates unchanged.
func mapSQLiteDeleteError(w http.ResponseWriter, logger *slog.Logger, err error) {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
		writeAPIError(w, http.StatusConflict, "referenced_by_other_records",
			"This record is referenced by other data and cannot be deleted.")
		return
	}
	mapSQLiteError(w, logger, err)
}
