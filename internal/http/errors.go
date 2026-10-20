package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
)

// errorEnvelope is the wire shape of every API error:
// {"error":{"code":"snake_case_slug","message":"An English sentence."}}.
//
// Both fields are English: the API is ADR-014's "code" surface, and the SPA
// maps code to the treasurer-facing Indonesian copy.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeAPIError writes the error envelope. Every failure goes through it, so
// the shape cannot drift route by route.
func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{Code: code, Message: message}})
}

// mapLedgerError answers an error from an internal/ledger call, per ADR-027's
// sentinel taxonomy. Every ledger-calling route shares this one switch.
func mapLedgerError(w http.ResponseWriter, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, ledger.ErrInvalidArgument):
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The request contains an invalid argument.")
	case errors.Is(err, ledger.ErrAmountTooLarge):
		// Rp 999.999.999.999 (money.MaxAmount, #499). Its own code, not
		// invalid_argument: the amount is well-formed and the SPA tells her the one
		// thing to change. 400 like other ledger input refusals; the message names
		// the limit, never the value sent.
		writeAPIError(w, http.StatusBadRequest, "amount_too_large", "An amount cannot be larger than 999,999,999,999 rupiah.")
	case errors.Is(err, ledger.ErrDuesRateSuperseded):
		// A correction is a new row with a new id (ADR-040), so a page holding the
		// old id is stale. 409, its own code: the SPA tells her to reload the list.
		writeAPIError(w, http.StatusConflict, "dues_rate_superseded", "This rate has since been corrected or removed; reload the list.")
	case errors.Is(err, ledger.ErrDuesRateMonthTaken):
		// The code POST has always answered for a month that already has a rate.
		writeAPIError(w, http.StatusConflict, "unique_violation", "This value conflicts with an existing record.")
	case errors.Is(err, ledger.ErrReimbursementWaived):
		writeAPIError(w, http.StatusConflict, "reimbursement_waived", "This reimbursement has been waived.")
	case errors.Is(err, ledger.ErrReimbursementAlreadySettled):
		writeAPIError(w, http.StatusConflict, "reimbursement_already_settled", "This reimbursement has already been settled.")
	case errors.Is(err, ledger.ErrDuesPaymentNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
	case errors.Is(err, sql.ErrNoRows):
		// A ledger method that fetches the row it is about before writing
		// (SettleReimbursement's GetReimbursement) wraps the driver's sql.ErrNoRows
		// when the id names nothing. That is a 404 for the reason
		// ErrDuesPaymentNotFound is: the id came from the path. Without this case it
		// reaches the default and answers 500 for a wrong id (#69).
		writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
	case errors.Is(err, ledger.ErrNotADuesPayment):
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "That transaction is not a dues payment and cannot be reversed.")
	case errors.Is(err, ledger.ErrDuesPaymentAlreadyReversed):
		writeAPIError(w, http.StatusConflict, "dues_payment_already_reversed", "This dues payment has already been reversed.")
	case errors.Is(err, ledger.ErrIncidentalAlreadyClosed):
		writeAPIError(w, http.StatusConflict, "incidental_already_closed", "This incidental has already been closed.")
	case errors.Is(err, ledger.ErrAccountHoldsMoney):
		writeAPIError(w, http.StatusConflict, "account_holds_money", "This location still holds money; move it out before retiring the location.")
	case errors.Is(err, ledger.ErrAccountBalanceNegative):
		writeAPIError(w, http.StatusConflict, "account_balance_negative", "This location's recorded balance is negative; reconcile it before retiring the location.")
	case errors.Is(err, ledger.ErrIncidentalClosed):
		writeAPIError(w, http.StatusConflict, "incidental_closed", "This incidental is closed and cannot accept new transactions.")
	case errors.Is(err, ledger.ErrIncidentalNotClosed):
		writeAPIError(w, http.StatusConflict, "incidental_not_closed", "This incidental is not closed.")
	case errors.Is(err, ledger.ErrFundAlreadyExists):
		writeAPIError(w, http.StatusConflict, "fund_already_exists", "A fund has already been set up.")
	case errors.Is(err, ledger.ErrPurposeCorrectionNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
	case errors.Is(err, ledger.ErrPurposeCorrectionOpening):
		writeAPIError(w, http.StatusConflict, "purpose_correction_opening", "An opening balance's purpose cannot be corrected.")
	case errors.Is(err, ledger.ErrPurposeCorrectionDues):
		writeAPIError(w, http.StatusConflict, "purpose_correction_dues", "A dues payment's purpose cannot be corrected.")
	case errors.Is(err, ledger.ErrPurposeCorrectionReimbursement):
		writeAPIError(w, http.StatusConflict, "purpose_correction_reimbursement", "A reimbursement payout's purpose cannot be corrected here - correct the claim itself instead.")
	case errors.Is(err, ledger.ErrPurposeCorrectionTransfer):
		writeAPIError(w, http.StatusConflict, "purpose_correction_transfer", "A transfer leg's purpose cannot be corrected.")
	case errors.Is(err, ledger.ErrPurposeCorrectionDuesReversal):
		writeAPIError(w, http.StatusConflict, "purpose_correction_dues_reversal", "A dues reversal's purpose cannot be corrected.")
	case errors.Is(err, ledger.ErrPurposeCorrectionTargetClosed):
		writeAPIError(w, http.StatusConflict, "purpose_correction_target_closed", "This incidental is closed - reopen it before correcting into it.")
	case errors.Is(err, ledger.ErrPurposeCorrectionSourceClosed):
		writeAPIError(w, http.StatusConflict, "purpose_correction_source_closed", "This incidental is closed - reopen it before correcting out of it.")
	case errors.Is(err, ledger.ErrPurposeCorrectionNoop):
		writeAPIError(w, http.StatusConflict, "purpose_correction_noop", "This row is already tagged to that purpose.")
	case errors.Is(err, ledger.ErrPurposeCorrectionNamedContribution):
		writeAPIError(w, http.StatusConflict, "purpose_correction_named_contribution", "A named contribution's purpose cannot be corrected - reverse it and post again instead.")
	case errors.Is(err, ledger.ErrPurposeMoveSamePurpose):
		writeAPIError(w, http.StatusBadRequest, "purpose_move_same_purpose", "A purpose move needs two different purposes.")
	case errors.Is(err, ledger.ErrPurposeMoveUnknownPurpose):
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The request references a purpose that does not exist.")
	case errors.Is(err, ledger.ErrPurposeMoveUnknownAccount):
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The request references an account that does not exist.")
	case errors.Is(err, ledger.ErrPurposeMovePassThrough):
		writeAPIError(w, http.StatusConflict, "purpose_move_pass_through", "Pass-through money cannot be moved between purposes.")
	case errors.Is(err, ledger.ErrPurposeMoveClosed):
		writeAPIError(w, http.StatusConflict, "purpose_move_closed", "This incidental is closed - reopen it before moving money into or out of it.")
	case errors.Is(err, ledger.ErrPurposeMoveInsufficient):
		writeAPIError(w, http.StatusConflict, "purpose_move_insufficient", "The source purpose holds less than that amount.")
	case errors.Is(err, ledger.ErrPurposeMoveAccountInactive):
		writeAPIError(w, http.StatusConflict, "purpose_move_account_inactive", "This account is inactive.")
	case errors.Is(err, ledger.ErrAccountInactive):
		// Any posting (and a Cek kas count) naming a retired location (#444).
		// PostPurposeMove keeps its purpose_move_account_inactive above.
		writeAPIError(w, http.StatusConflict, "account_inactive", "This location has been retired and cannot take new entries.")
	case errors.Is(err, ledger.ErrReconciliationMissingLocation):
		writeAPIError(w, http.StatusConflict, "reconciliation_location_missing", "A cash count must include every active location.")
	case errors.Is(err, ledger.ErrReconciliationPlugMismatch):
		writeAPIError(w, http.StatusConflict, "reconciliation_plug_mismatch", "An adjusting entry must equal the difference exactly.")
	case errors.Is(err, ledger.ErrContributionRequiresIncidentalPurpose):
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "A named contribution must be tagged to an incidental (envelope) purpose.")
	case errors.Is(err, money.ErrOverflow):
		// err's message embeds the operands that overflowed - the amounts - which
		// ADR-022 forbids logging. Other unrecognized errors reaching the default
		// wrap an id, not an amount (ADR-027), so only this case withholds the message.
		logger.Error("unhandled ledger error", "kind", "overflow")
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
	default:
		// A constraint the caller's input tripped, not a domain bug. ADR-027 sent
		// every schema violation from the ledger to a 500, on the premise that the
		// ids arrive from earlier Querier calls the domain made. UpdateReimbursement
		// (#103) broke that: a PATCH body carries member_id and purpose_id from the
		// client, so a FOREIGN KEY violation is a typo and a 500 would blame the
		// server. Classification is mapSQLiteError's; only which errors reach it is
		// new (ADR-027's Amendments).
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) {
			mapSQLiteError(w, logger, err)
			return
		}
		logger.Error("unhandled ledger error", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
	}
}

// mapAuthError answers an error from an internal/auth call, per its sentinel
// taxonomy, one switch per domain package like mapLedgerError.
func mapAuthError(w http.ResponseWriter, logger *slog.Logger, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidArgument):
		writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The request contains an invalid argument.")
	case errors.Is(err, auth.ErrAlreadyRegistered):
		writeAPIError(w, http.StatusConflict, "already_registered", "An account has already been registered on this instance.")
	case errors.Is(err, auth.ErrInvalidCredentials):
		// The one body POST /api/login answers on any failed login, unknown email or
		// wrong password alike (#115): fixed code and message, never interpolating
		// the case, so the response bytes cannot become the oracle Authenticate
		// forbids.
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password.")
	case errors.Is(err, auth.ErrWrongCurrentPassword):
		// The current password typed to change the login's email or password (#386).
		// Its own code, not invalid_credentials: the caller holds a session, so
		// there is no account-existence oracle to protect, and the SPA words it for
		// that dialog. 403, not 401: she is signed in, only not allowed this.
		writeAPIError(w, http.StatusForbidden, "wrong_current_password", "The current password is incorrect.")
	case errors.Is(err, auth.ErrBusy):
		// Every password-hashing slot stayed taken for the whole wait
		// (auth/password_gate.go, #495). 503, not 429: the server is saturated, not
		// this caller over a limit, and 429 already means "you, wait minutes" to the
		// SPA (too_many_requests). Its own code, so the copy says "busy, try again in
		// a moment". Retry-After: the wait was seconds.
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, http.StatusServiceUnavailable, "server_busy", "The server is busy. Try again in a moment.")
	default:
		logger.Error("unhandled auth error", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
	}
}

// mapLedgerDeleteError is mapLedgerError for a DELETE through the ledger,
// mirroring the mapSQLiteError / mapSQLiteDeleteError pair:
// SQLITE_CONSTRAINT_FOREIGNKEY means "you named a row that doesn't exist" on
// a write and "real data still points at this row" on a delete (400 and 409,
// one result code).
//
// The ledger sentinels are checked first and identically, so a settled claim
// is still its named 409.
func mapLedgerDeleteError(w http.ResponseWriter, logger *slog.Logger, err error) {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
		writeAPIError(w, http.StatusConflict, "referenced_by_other_records",
			"This record is referenced by other data and cannot be deleted.")
		return
	}
	mapLedgerError(w, logger, err)
}

// mapSQLiteError answers an error from a direct-CRUD route, one calling
// store.Queries rather than internal/ledger, so none of ADR-027's sentinels
// apply. It reads the driver's result code.
//
// errors.As against *sqlite.Error and Code() against the modernc.org/sqlite/lib
// constants, never a string match: the message is not stable across driver
// releases, the extended result code is.
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
			// A reference to a row that does not exist, e.g. a member's tier_id naming no
			// dues_tier. #63's foreign_keys=ON (internal/db) makes this reachable;
			// without it SQLite would accept the row. 400, not 404: the id is a field on
			// the request body, not a path segment naming the resource.
			writeAPIError(w, http.StatusBadRequest, "invalid_argument", "The request references a record that does not exist.")
			return
		}
	}

	logger.Error("unhandled sqlite error", "error", err)
	writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
}

// mapSQLiteDeleteError is mapSQLiteError for a DELETE. SQLITE_CONSTRAINT_FOREIGNKEY
// has two opposite meanings and one result code, so the handler distinguishes
// them: on a create or update, "you named a row that doesn't exist" (400); on
// a delete, the row exists and real data points at it (a member with posted
// transactions), a conflict. Every other class means the same either way and
// delegates unchanged.
func mapSQLiteDeleteError(w http.ResponseWriter, logger *slog.Logger, err error) {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY {
		writeAPIError(w, http.StatusConflict, "referenced_by_other_records",
			"This record is referenced by other data and cannot be deleted.")
		return
	}
	mapSQLiteError(w, logger, err)
}
