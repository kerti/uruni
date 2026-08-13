package ledger

import "errors"

// ErrInvalidArgument is the fourth error category ADR-027 adds to the three
// this package's write methods otherwise produce (money.ErrOverflow, a raw
// database error, and the business-state sentinels later slices define): the
// caller's own input failed a shape check before the write ever reached the
// schema - a non-positive amount, an occurred_on that is not a real calendar
// date, an empty required field.
//
// Every returned error wraps this with %w and names the offending field, so a
// caller branches with errors.Is(err, ErrInvalidArgument) rather than matching
// a string, and M4 maps it to 400. It also keeps this package safe to call
// from outside an HTTP handler - ADR-012's import is the next caller, and it
// has no request-validation layer in front of it at all.
//
// What this is deliberately not for: composite-FK violations, an account
// belonging to another fund, an unrecognized id - anything the schema already
// enforces. Those are domain bugs, not caller mistakes (the ids involved come
// from earlier Querier calls this package itself made), and surface wrapped
// generically for M4 to map to a 500. Re-deriving a cross-row invariant here
// would be a second source of truth for something the schema already answers
// (ADR-027).
var ErrInvalidArgument = errors.New("ledger: invalid argument")

// ErrOpeningBalanceExists is returned by PostOpeningBalance when the account
// already carries a kind='opening' row.
//
// The schema's opening_balance_once_per_account partial unique index is the
// actual guarantee - a second opening row cannot exist once the write reaches
// it, under any caller, including one that bypasses this package entirely.
// PostOpeningBalance's pre-check exists only to turn that into a clean, named
// error instead of a raw "UNIQUE constraint failed" string, exactly as
// ADR-027 describes for SettleReimbursement's settled-once check: under
// ADR-004's SetMaxOpenConns(1), a race between the pre-check and the insert
// is structurally impossible, so this is not a lock and closes no window the
// index does not already close.
var ErrOpeningBalanceExists = errors.New("ledger: opening balance already exists for this account")

// ErrReimbursementWaived is returned by SettleReimbursement when the claim's
// waived_on is set: a claim the treasurer has already written off as never
// going to be repaid cannot also be settled.
var ErrReimbursementWaived = errors.New("ledger: reimbursement has been waived")

// ErrReimbursementAlreadySettled is returned by SettleReimbursement when
// GetReimbursementSettlement finds an existing kind='reimbursement' row for
// the claim.
//
// The schema's reimbursement_settled_once partial unique index is the actual
// guarantee - a second settling row cannot exist once the write reaches it,
// under any caller, including one that bypasses this package entirely.
// SettleReimbursement's pre-check exists only to turn that into a clean,
// named error instead of a raw "UNIQUE constraint failed" string, exactly as
// ADR-027 describes: under ADR-004's SetMaxOpenConns(1), a race between the
// pre-check and the insert is structurally impossible, so this is not a lock
// and closes no window the index does not already close.
var ErrReimbursementAlreadySettled = errors.New("ledger: reimbursement has already been settled")
