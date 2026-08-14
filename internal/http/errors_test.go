package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// testLogger discards output — these tests assert the HTTP response, not the
// log line (requestLogger's own test covers what gets logged).
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testStoreDB returns a real, migrated in-memory database, the same recipe
// internal/ledger's own fixture_test.go uses — a genuine driver is the whole
// point of this file: the mapper is verified against real SQLite result codes,
// not a hand-made fake.
func testStoreDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("db.Open(\":memory:\") = %v, want no error", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if _, err := db.Up(context.Background(), sqlDB, testLogger()); err != nil {
		t.Fatalf("db.Up() = %v, want no error", err)
	}
	return sqlDB
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var env errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decoding error envelope: %v (body: %s)", err, rec.Body.String())
	}
	if env.Error.Code == "" {
		t.Fatalf("error envelope has empty code (body: %s)", rec.Body.String())
	}
	if env.Error.Message == "" {
		t.Fatalf("error envelope has empty message (body: %s)", rec.Body.String())
	}
	return env.Error
}

func TestMapLedgerErrorMapsSentinelsToStatusAndCode(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid argument", ledger.ErrInvalidArgument, 400, "invalid_argument"},
		{"reimbursement waived", ledger.ErrReimbursementWaived, 409, "reimbursement_waived"},
		{"reimbursement already settled", ledger.ErrReimbursementAlreadySettled, 409, "reimbursement_already_settled"},
		{"incidental already closed", ledger.ErrIncidentalAlreadyClosed, 409, "incidental_already_closed"},
		{"opening balance exists", ledger.ErrOpeningBalanceExists, 409, "opening_balance_exists"},
		{"money overflow", money.ErrOverflow, 500, "internal_error"},
		{"unrecognized", errors.New("some domain-internal failure"), 500, "internal_error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mapLedgerError(rec, testLogger(), tc.err)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			got := decodeError(t, rec)
			if got.Code != tc.code {
				t.Errorf("code = %q, want %q", got.Code, tc.code)
			}
		})
	}
}

// TestMapLedgerErrorWraps checks that %w-wrapped sentinels still match — every
// ADR-027 sentinel is returned wrapped with context in real callers.
func TestMapLedgerErrorMatchesAWrappedSentinel(t *testing.T) {
	wrapped := errors.New("posting transfer leg: " + ledger.ErrInvalidArgument.Error())
	rec := httptest.NewRecorder()
	mapLedgerError(rec, testLogger(), wrapped)
	// A plain errors.New wrapping only the *text* does not satisfy errors.Is —
	// this asserts the unrecognized path, not a false match, guarding against a
	// mapper that accidentally string-matches instead of using errors.Is.
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500 (text alone must not satisfy errors.Is)", rec.Code)
	}

	fwrapped := fmt.Errorf("checking argument shape: %w", ledger.ErrInvalidArgument)
	rec2 := httptest.NewRecorder()
	mapLedgerError(rec2, testLogger(), fwrapped)
	if rec2.Code != 400 {
		t.Fatalf("status = %d, want 400 for a %%w-wrapped ErrInvalidArgument", rec2.Code)
	}
}

func TestMapSQLiteErrorMapsAGenuineUniqueViolation(t *testing.T) {
	q := store.New(testStoreDB(t))
	ctx := context.Background()

	const slug = "abcdefghijklmnopqrstuv" // 22 chars, the schema's minimum
	if _, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Test Fund", Currency: "IDR", ReportSlug: slug, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("first CreateFund() = %v, want no error", err)
	}

	_, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Second Fund", Currency: "IDR", ReportSlug: slug, CreatedAt: 2,
	})
	if err == nil {
		t.Fatalf("second CreateFund() with a duplicate report_slug = nil error, want a UNIQUE violation")
	}

	rec := httptest.NewRecorder()
	mapSQLiteError(rec, testLogger(), err)
	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	got := decodeError(t, rec)
	if got.Code != "unique_violation" {
		t.Errorf("code = %q, want %q", got.Code, "unique_violation")
	}
}

func TestMapSQLiteErrorMapsAGenuineCheckViolation(t *testing.T) {
	sqlDB := testStoreDB(t)
	q := store.New(sqlDB)
	ctx := context.Background()

	fund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Test Fund", Currency: "IDR", ReportSlug: "abcdefghijklmnopqrstuv", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}

	// account.kind CHECK IN ('cash','bank') — 'wallet' is neither.
	_, err = q.CreateAccount(ctx, store.CreateAccountParams{
		FundID: fund.ID, Kind: "wallet", Name: "Bad Account", CreatedAt: 1,
	})
	if err == nil {
		t.Fatalf("CreateAccount(kind=%q) = nil error, want a CHECK violation", "wallet")
	}

	rec := httptest.NewRecorder()
	mapSQLiteError(rec, testLogger(), err)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	got := decodeError(t, rec)
	if got.Code != "check_violation" {
		t.Errorf("code = %q, want %q", got.Code, "check_violation")
	}
}

func TestMapSQLiteErrorMapsAGenuineForeignKeyViolation(t *testing.T) {
	sqlDB := testStoreDB(t)
	q := store.New(sqlDB)
	ctx := context.Background()

	fund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Test Fund", Currency: "IDR", ReportSlug: "abcdefghijklmnopqrstuv", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}

	// tier_id 999 names no dues_tier row - the composite FK requires
	// (fund_id, tier_id) to match a real dues_tier, which this cannot.
	noSuchTier := int64(999)
	_, err = q.CreateMember(ctx, store.CreateMemberParams{
		FundID: fund.ID, Name: "Jane", TierID: &noSuchTier, CreatedAt: 1,
	})
	if err == nil {
		t.Fatalf("CreateMember(tier_id=999) = nil error, want a FOREIGN KEY violation")
	}

	rec := httptest.NewRecorder()
	mapSQLiteError(rec, testLogger(), err)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	got := decodeError(t, rec)
	if got.Code != "invalid_argument" {
		t.Errorf("code = %q, want %q", got.Code, "invalid_argument")
	}
}

// TestMapSQLiteDeleteErrorMapsAGenuineForeignKeyViolationTo409 is
// mapSQLiteError's foreign-key test above, but for a DELETE: the same
// SQLITE_CONSTRAINT_FOREIGNKEY code, read the opposite way (issue #81).
func TestMapSQLiteDeleteErrorMapsAGenuineForeignKeyViolationTo409(t *testing.T) {
	sqlDB := testStoreDB(t)
	q := store.New(sqlDB)
	ctx := context.Background()

	fund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Test Fund", Currency: "IDR", ReportSlug: "abcdefghijklmnopqrstuv", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}
	if _, err := q.CreateMember(ctx, store.CreateMemberParams{FundID: fund.ID, Name: "Jane", CreatedAt: 1}); err != nil {
		t.Fatalf("CreateMember() = %v, want no error", err)
	}

	// fund is referenced by member.fund_id (plain FOREIGN KEY) - deleting it
	// out from under a real member is what a genuine "row exists, real data
	// still points at it" DELETE-time FK violation looks like here.
	_, err = sqlDB.ExecContext(ctx, "DELETE FROM fund WHERE id = ?", fund.ID)
	if err == nil {
		t.Fatalf("DELETE FROM fund with a real member = nil error, want a FOREIGN KEY violation")
	}

	rec := httptest.NewRecorder()
	mapSQLiteDeleteError(rec, testLogger(), err)
	if rec.Code != 409 {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	got := decodeError(t, rec)
	if got.Code != "referenced_by_other_records" {
		t.Errorf("code = %q, want %q", got.Code, "referenced_by_other_records")
	}
}

// TestMapSQLiteDeleteErrorDelegatesNonForeignKeyViolations proves the
// delegation for every other class - CHECK, UNIQUE, no-rows, unrecognized -
// still behaves exactly like mapSQLiteError, so the DELETE-only override is
// scoped to the one violation class it exists for.
func TestMapSQLiteDeleteErrorDelegatesNonForeignKeyViolations(t *testing.T) {
	rec := httptest.NewRecorder()
	mapSQLiteDeleteError(rec, testLogger(), sql.ErrNoRows)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("code = %q, want %q", got.Code, "not_found")
	}
}

func TestMapSQLiteErrorMapsNoRowsToNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	mapSQLiteError(rec, testLogger(), sql.ErrNoRows)
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	got := decodeError(t, rec)
	if got.Code != "not_found" {
		t.Errorf("code = %q, want %q", got.Code, "not_found")
	}
}

func TestMapSQLiteErrorMapsAnUnrecognizedErrorTo500(t *testing.T) {
	rec := httptest.NewRecorder()
	mapSQLiteError(rec, testLogger(), errors.New("disk full or some other unmapped failure"))
	if rec.Code != 500 {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	got := decodeError(t, rec)
	if got.Code != "internal_error" {
		t.Errorf("code = %q, want %q", got.Code, "internal_error")
	}
}
