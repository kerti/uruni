package http

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// testLogger discards output: these tests assert the HTTP response, not the
// log line (requestLogger's test covers that).
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testStoreDB returns a real, migrated in-memory database (the recipe of
// internal/ledger's fixture_test.go): a genuine driver is the point, verifying
// the mapper against real SQLite result codes, not a fake.
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
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid argument", ledger.ErrInvalidArgument, 400, "invalid_argument"},
		{"amount above the ceiling", ledger.ErrAmountTooLarge, 400, "amount_too_large"},
		{"amount above the ceiling, wrapped", fmt.Errorf("posting transaction: %w", ledger.CheckAmountCeiling("amount", money.MaxAmount+1)), 400, "amount_too_large"},
		{"date outside the window", ledger.ErrDateOutOfRange, 400, "date_out_of_range"},
		{"date outside the window, wrapped", fmt.Errorf("posting transaction: %w: occurred_on is after 2026-10-20", ledger.ErrDateOutOfRange), 400, "date_out_of_range"},
		{"idempotency key reused", ledger.ErrIdempotencyKeyReused, 409, "idempotency_key_reused"},
		{"reimbursement waived", ledger.ErrReimbursementWaived, 409, "reimbursement_waived"},
		{"reimbursement already settled", ledger.ErrReimbursementAlreadySettled, 409, "reimbursement_already_settled"},
		{"incidental already closed", ledger.ErrIncidentalAlreadyClosed, 409, "incidental_already_closed"},
		{"retired location", ledger.ErrAccountInactive, 409, "account_inactive"},
		{"retired location, wrapped", fmt.Errorf("posting transaction: %w", ledger.ErrAccountInactive), 409, "account_inactive"},
		{"cek kas omits an active location", ledger.ErrReconciliationMissingLocation, 409, "reconciliation_location_missing"},
		{"cek kas plug is not the difference", ledger.ErrReconciliationPlugMismatch, 409, "reconciliation_plug_mismatch"},
		{"purpose move keeps its own retired-location code", ledger.ErrPurposeMoveAccountInactive, 409, "purpose_move_account_inactive"},
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

// The constraint refusals the ledger names answer with the sentences the
// driver-error mapping gave the same routes before they moved below HTTP: the
// SPA and the API's readers hold the message too.
func TestLedgerConstraintRefusalsKeepTheirWireMessages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err     error
		message string
	}{
		{ledger.ErrDataConstraint, "The request violates a data constraint."},
		{ledger.ErrValueTaken, "This value conflicts with an existing record."},
		{ledger.ErrReferenceMissing, "The request references a record that does not exist."},
		{ledger.ErrReferencedByOtherRecords, "This record is referenced by other data and cannot be deleted."},
		{ledger.ErrPurposeNotRenameable, "The fund's Kas Utama purpose cannot be renamed."},
	}
	for _, tc := range cases {
		t.Run(tc.err.Error(), func(t *testing.T) {
			rec := httptest.NewRecorder()
			mapLedgerError(rec, testLogger(), fmt.Errorf("deleting account: %w", tc.err))
			if got := decodeError(t, rec); got.Message != tc.message {
				t.Errorf("message = %q, want %q", got.Message, tc.message)
			}
		})
	}
}

// TestMapAuthErrorMapsSentinelsToStatusAndCode is mapLedgerError's twin for
// internal/auth. The unrecognized arm matters most: a database fault out of
// Register must read 500, never the 409 telling the treasurer an account
// exists when none does.
func TestMapAuthErrorMapsSentinelsToStatusAndCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid argument", auth.ErrInvalidArgument, 400, "invalid_argument"},
		{"already registered", auth.ErrAlreadyRegistered, 409, "already_registered"},
		{"wrapped sentinel", fmt.Errorf("registering: %w", auth.ErrAlreadyRegistered), 409, "already_registered"},
		{"unrecognized", errors.New("some database failure"), 500, "internal_error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mapAuthError(rec, testLogger(), tc.err)

			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			if got := decodeError(t, rec); got.Code != tc.code {
				t.Errorf("code = %q, want %q", got.Code, tc.code)
			}
		})
	}
}

// TestMapLedgerErrorMatchesAWrappedSentinel: %w-wrapped sentinels still match;
// real callers return every ADR-027 sentinel wrapped with context.
func TestMapLedgerErrorMatchesAWrappedSentinel(t *testing.T) {
	t.Parallel()
	wrapped := errors.New("posting transfer leg: " + ledger.ErrInvalidArgument.Error())
	rec := httptest.NewRecorder()
	mapLedgerError(rec, testLogger(), wrapped)
	// A plain errors.New wrapping only the text does not satisfy errors.Is: this
	// asserts the unrecognized path, guarding against a mapper that string-matches.
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
	t.Parallel()
	q := store.New(testStoreDB(t))
	ctx := context.Background()

	const slug = "abcdefghijklmnopqrstuv" // 22 chars, the schema's minimum
	if _, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Test Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: slug, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("first CreateFund() = %v, want no error", err)
	}

	_, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Second Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: slug, CreatedAt: 2,
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
	t.Parallel()
	sqlDB := testStoreDB(t)
	q := store.New(sqlDB)
	ctx := context.Background()

	fund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Test Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "abcdefghijklmnopqrstuv", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}

	// account.kind CHECK IN ('cash','bank') - 'wallet' is neither.
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
	t.Parallel()
	sqlDB := testStoreDB(t)
	q := store.New(sqlDB)
	ctx := context.Background()

	fund, err := q.CreateFund(ctx, store.CreateFundParams{
		Name: "Test Fund", Currency: "IDR", TimeZone: "Asia/Jakarta", ReportSlug: "abcdefghijklmnopqrstuv", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateFund() = %v, want no error", err)
	}

	// tier_id 999 names no dues_tier row: the composite FK requires (fund_id,
	// tier_id) to match a real one.
	noSuchTier := int64(999)
	jane, err := q.CreateMember(ctx, store.CreateMemberParams{JoinedOn: "2000-01-01",
		FundID: fund.ID, Name: "Jane", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateMember() = %v, want no error", err)
	}
	_, err = q.CreateMemberTier(ctx, store.CreateMemberTierParams{
		FundID: fund.ID, MemberID: jane.ID, TierID: &noSuchTier, EffectiveFrom: "2000-01", CreatedAt: 1,
	})
	if err == nil {
		t.Fatalf("CreateMemberTier(tier_id=999) = nil error, want a FOREIGN KEY violation")
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

func TestMapSQLiteErrorMapsNoRowsToNotFound(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// #667: an error that is only the request's context being canceled is the
// client going away, not a server fault - 499 and an INFO line, never a 500
// and an ERROR, from every general fallback. A deadline stays the server's.
func TestMapErrorsAnswerACanceledRequestWith499AndNoErrorLine(t *testing.T) {
	t.Parallel()
	canceled := fmt.Errorf("listing rates: %w", context.Canceled)
	mappers := map[string]func(http.ResponseWriter, *slog.Logger, error){
		"mapLedgerError": mapLedgerError,
		"mapAuthError":   mapAuthError,
		"mapSQLiteError": mapSQLiteError,
	}
	for name, mapErr := range mappers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			rec := httptest.NewRecorder()
			mapErr(rec, slog.New(slog.NewTextHandler(&logs, nil)), canceled)
			if rec.Code != statusClientClosedRequest {
				t.Errorf("status = %d, want %d", rec.Code, statusClientClosedRequest)
			}
			if strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "level=INFO") {
				t.Errorf("log = %q, want one INFO line and no ERROR", logs.String())
			}
		})
	}

	t.Run("a deadline is still a 500", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		mapSQLiteError(rec, testLogger(), fmt.Errorf("listing rates: %w", context.DeadlineExceeded))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", rec.Code)
		}
	})
}

// TestEveryLedgerErrorKeepsItsWireStatusAndCode pins each domain error's answer.
// A ledger.Error declares its own class and code, so nothing else would notice
// a refusal moving from 409 to 400: changing one here is a wire change, made on
// purpose. A new error needs its row.
func TestEveryLedgerErrorKeepsItsWireStatusAndCode(t *testing.T) {
	t.Parallel()
	type answer struct {
		status int
		code   string
	}
	want := map[*ledger.Error]answer{
		ledger.ErrAmountTooLarge:                        {400, "amount_too_large"},
		ledger.ErrDateOutOfRange:                        {400, "date_out_of_range"},
		ledger.ErrInvalidArgument:                       {400, "invalid_argument"},
		ledger.ErrReimbursementWaived:                   {409, "reimbursement_waived"},
		ledger.ErrReimbursementAlreadySettled:           {409, "reimbursement_already_settled"},
		ledger.ErrFundAlreadyExists:                     {409, "fund_already_exists"},
		ledger.ErrReversalNotFound:                      {404, "not_found"},
		ledger.ErrReversalOpening:                       {409, "reversal_opening"},
		ledger.ErrReversalReimbursement:                 {409, "reversal_reimbursement"},
		ledger.ErrReversalTransfer:                      {409, "reversal_transfer"},
		ledger.ErrReversalOfReversal:                    {409, "reversal_of_reversal"},
		ledger.ErrReversalAdjustment:                    {409, "reversal_adjustment"},
		ledger.ErrReversalReconciliationFix:             {409, "reversal_reconciliation_fix"},
		ledger.ErrAlreadyReversed:                       {409, "transaction_already_reversed"},
		ledger.ErrReversalBeforeOriginal:                {409, "reversal_before_original"},
		ledger.ErrIncidentalClosed:                      {409, "incidental_closed"},
		ledger.ErrContributionRequiresIncidentalPurpose: {400, "invalid_argument"},
		ledger.ErrIncidentalNotClosed:                   {409, "incidental_not_closed"},
		ledger.ErrIncidentalAlreadyClosed:               {409, "incidental_already_closed"},
		ledger.ErrPurposeCorrectionNotFound:             {404, "not_found"},
		ledger.ErrPurposeCorrectionOpening:              {409, "purpose_correction_opening"},
		ledger.ErrPurposeCorrectionDues:                 {409, "purpose_correction_dues"},
		ledger.ErrPurposeCorrectionReimbursement:        {409, "purpose_correction_reimbursement"},
		ledger.ErrPurposeCorrectionTransfer:             {409, "purpose_correction_transfer"},
		ledger.ErrPurposeCorrectionReversal:             {409, "purpose_correction_reversal"},
		ledger.ErrPurposeCorrectionAlreadyReversed:      {409, "purpose_correction_already_reversed"},
		ledger.ErrPurposeCorrectionNoop:                 {409, "purpose_correction_noop"},
		ledger.ErrPurposeCorrectionNamedContribution:    {409, "purpose_correction_named_contribution"},
		ledger.ErrPurposeCorrectionTargetClosed:         {409, "purpose_correction_target_closed"},
		ledger.ErrPurposeCorrectionSourceClosed:         {409, "purpose_correction_source_closed"},
		ledger.ErrPurposeMoveSamePurpose:                {400, "purpose_move_same_purpose"},
		ledger.ErrPurposeMoveUnknownPurpose:             {400, "invalid_argument"},
		ledger.ErrPurposeMovePassThrough:                {409, "purpose_move_pass_through"},
		ledger.ErrPurposeMoveClosed:                     {409, "purpose_move_closed"},
		ledger.ErrPurposeMoveInsufficient:               {409, "purpose_move_insufficient"},
		ledger.ErrPurposeMoveUnknownAccount:             {400, "invalid_argument"},
		ledger.ErrPurposeMoveAccountInactive:            {409, "purpose_move_account_inactive"},
		ledger.ErrAccountInactive:                       {409, "account_inactive"},
		ledger.ErrAccountHoldsMoney:                     {409, "account_holds_money"},
		ledger.ErrAccountBalanceNegative:                {409, "account_balance_negative"},
		ledger.ErrReconciliationMissingLocation:         {409, "reconciliation_location_missing"},
		ledger.ErrReconciliationPlugMismatch:            {409, "reconciliation_plug_mismatch"},
		ledger.ErrDuesRateSuperseded:                    {409, "dues_rate_superseded"},
		ledger.ErrDuesRateMonthTaken:                    {409, "unique_violation"},
		ledger.ErrIdempotencyKeyReused:                  {409, "idempotency_key_reused"},
		ledger.ErrDataConstraint:                        {400, "check_violation"},
		ledger.ErrValueTaken:                            {409, "unique_violation"},
		ledger.ErrReferenceMissing:                      {400, "invalid_argument"},
		ledger.ErrReferencedByOtherRecords:              {409, "referenced_by_other_records"},
		ledger.ErrPurposeNotRenameable:                  {409, "purpose_not_renameable"},
	}
	errs := ledger.Errors()
	if len(errs) != len(want) {
		t.Errorf("ledger.Errors() has %d errors, the table %d: add or remove the row", len(errs), len(want))
	}
	for _, e := range errs {
		w, ok := want[e]
		if !ok {
			t.Errorf("%q: no row in the table", e.Error())
			continue
		}
		if got := (answer{ledgerStatus(e.Class()), e.Code()}); got != w {
			t.Errorf("%q answers %d %s, want %d %s", e.Error(), got.status, got.code, w.status, w.code)
		}
	}
}
