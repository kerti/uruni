package http

import (
	"context"
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
// {"error":{"code":"snake_case_slug","message":"An English sentence."}}, plus
// "field" on an invalid_argument that one request field is at fault for (#530):
// its JSON key, dotted when nested ("counts.fix.amount"), or the query
// parameter's name.
//
// Both fields are English: the API is ADR-014's "code" surface, and the SPA
// maps code to the treasurer-facing Indonesian copy.
type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// writeAPIError writes the error envelope. Every failure goes through it, so
// the shape cannot drift route by route.
func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeAPIFieldError(w, status, code, "", message)
}

// writeAPIFieldError is writeAPIError naming the request field at fault.
func writeAPIFieldError(w http.ResponseWriter, status int, code, field, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorEnvelope{Error: errorBody{Code: code, Message: message, Field: field}})
}

// invalidFieldOf is the request field a domain package's FieldError names, or
// "" (ledger's and auth's both answer InvalidField).
func invalidFieldOf(err error) string {
	var named interface{ InvalidField() string }
	if errors.As(err, &named) {
		return named.InvalidField()
	}
	return ""
}

// writeInvalidArgument answers auth's ErrInvalidArgument, with the field its
// FieldError names, if any.
func writeInvalidArgument(w http.ResponseWriter, err error) {
	writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", invalidFieldOf(err), "The request contains an invalid argument.")
}

// ledgerStatus is the one place a ledger.Class becomes an HTTP status.
func ledgerStatus(c ledger.Class) int {
	switch c {
	case ledger.ClassInvalid:
		return http.StatusBadRequest
	case ledger.ClassNotFound:
		return http.StatusNotFound
	case ledger.ClassConflict:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// statusClientClosedRequest is nginx's 499: the request's context was
// canceled before the answer was written. No client reads it; it is what the
// request log records.
const statusClientClosedRequest = 499

// answeredClientGone handles an error that is only the request's context being
// canceled - the client reloaded, navigated away or lost its connection, or
// shutdown ended the request (#667). That is not a server fault, so it is
// logged at INFO and recorded as 499 rather than an ERROR and a 500. A
// deadline is left to the caller: it is the server's own time limit.
func answeredClientGone(w http.ResponseWriter, logger *slog.Logger, err error) bool {
	if !errors.Is(err, context.Canceled) {
		return false
	}
	logger.Info("request canceled before it finished", "error", err)
	w.WriteHeader(statusClientClosedRequest)
	return true
}

// mapLedgerError answers an error from an internal/ledger call, per ADR-027's
// sentinel taxonomy. Every ledger-calling route shares this one function.
//
// A ledger.Error says its own wire code, message and class, so a new refusal
// needs nothing here. What follows it are the failures that are not ledger
// domain errors: a numeric overflow, a missing row, a raw driver error.
func mapLedgerError(w http.ResponseWriter, logger *slog.Logger, err error) {
	if answeredClientGone(w, logger, err) {
		return
	}
	var refusal *ledger.Error
	if errors.As(err, &refusal) {
		if errors.Is(err, ledger.ErrIdempotencyKeyReused) {
			// The app cannot produce this (ADR-043: a definite answer drops the key and
			// a new request mints another), so someone is reusing keys by hand: worth a
			// line, never the key or the body.
			logger.Warn("an idempotency key was reused for another request")
		}
		writeAPIFieldError(w, ledgerStatus(refusal.Class()), refusal.Code(), invalidFieldOf(err), refusal.Message())
		return
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// A ledger method that fetches the row it is about before writing
		// (SettleReimbursement's GetReimbursement) wraps the driver's sql.ErrNoRows
		// when the id names nothing. That is a 404 for the reason a ledger NotFound
		// is: the id came from the path. Without this case it reaches the default
		// and answers 500 for a wrong id (#69).
		writeAPIError(w, http.StatusNotFound, "not_found", "The requested resource was not found.")
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
	if answeredClientGone(w, logger, err) {
		return
	}
	switch {
	case errors.Is(err, auth.ErrPasswordTooCommon):
		// Before ErrInvalidArgument, which it wraps: the SPA has its own sentence
		// for a password on the common-passwords list (#528).
		writeAPIError(w, http.StatusBadRequest, "password_too_common", "This password is too common.")
	case errors.Is(err, auth.ErrInvalidArgument):
		writeInvalidArgument(w, err)
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

// mapSQLiteError answers an error from a direct-CRUD route, one calling
// store.Queries rather than internal/ledger, so none of ADR-027's sentinels
// apply. It reads the driver's result code.
//
// errors.As against *sqlite.Error and Code() against the modernc.org/sqlite/lib
// constants, never a string match: the message is not stable across driver
// releases, the extended result code is.
func mapSQLiteError(w http.ResponseWriter, logger *slog.Logger, err error) {
	if answeredClientGone(w, logger, err) {
		return
	}
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
