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

// ErrFundAlreadyExists is returned by SetUpFund when a fund already exists.
//
// Unlike ErrReimbursementAlreadySettled and ErrDuesPaymentAlreadyReversed, this is
// not a pre-check ahead of a unique index the schema already enforces: there
// is deliberately no such index. "At most one fund" is application policy,
// not a schema-level fact - PRD section 6 keeps multiple funds open at the *model*
// level, and a CHECK or a partial unique index baked into `fund` would need a
// migration to lift later if that policy ever changes. So this pre-check
// inside SetUpFund's own withTx is the entire guarantee, the same shape as
// ErrIncidentalAlreadyClosed's. What makes it honest rather than a check with
// a race hiding behind it: ADR-004's SetMaxOpenConns(1) already rules out two
// concurrent writers on this process, and #62's single-instance lock rules
// out a second `uruni serve` process against the same database file - the
// thing SetMaxOpenConns(1) alone cannot reach, since it protects one
// *sql.DB, not one file on disk.
var ErrFundAlreadyExists = errors.New("ledger: a fund already exists")

// ErrDuesPaymentNotFound is returned by ReverseDuesPayment when
// GetTransactionForFund finds no row for the given fund and transaction id.
//
// The fetch is fund-scoped (WHERE fund_id = ? AND id = ?), not id alone, so a
// transaction id that is real but belongs to another fund answers with this
// same sentinel rather than being found and only then rejected for ownership
// (ADR-029). That is what makes the composite FK on reverses_transaction_id
// load-bearing rather than decorative: a treasurer of one fund cannot even
// name a row belonging to another as the payment they are reversing, because
// the lookup that would name it never finds it in the first place.
var ErrDuesPaymentNotFound = errors.New("ledger: no such transaction")

// ErrNotADuesPayment is returned by ReverseDuesPayment when the fetched row's
// Kind is not "dues".
//
// This also rules out reversing a reversal: a reversal itself is posted as
// kind='adjustment' (ADR-029), never kind='dues', so it fails this same
// check rather than needing a separate one.
var ErrNotADuesPayment = errors.New("ledger: transaction is not a dues payment")

// ErrDuesPaymentAlreadyReversed is returned by ReverseDuesPayment when
// GetDuesPaymentReversal finds an existing reversal row for the payment.
//
// The schema's dues_payment_reversed_once partial unique index is the actual
// guarantee - a second reversal of the same payment cannot exist once the
// write reaches it, under any caller, including one that bypasses this
// package entirely. This pre-check exists only to turn that into a clean,
// named error instead of a raw "UNIQUE constraint failed" string, exactly as
// ADR-027 describes for ErrReimbursementAlreadySettled: under ADR-004's
// SetMaxOpenConns(1), a race between the pre-check and the insert is
// structurally impossible, so this is not a lock and closes no window the
// index does not already close.
var ErrDuesPaymentAlreadyReversed = errors.New("ledger: dues payment has already been reversed")

// ErrIncidentalClosed is returned by PostTransaction when PurposeID names an
// incidental whose closed_on is set (ADR-031).
//
// This supersedes the consequence ADR-027 originally accepted - "the purpose
// it closes stays open to new postings even afterward" - once this guard's
// code lands. It is symmetric, both directions: a late bill deserves
// attribution to the occasion exactly as much as a late contribution does,
// and it is a read-before-write business-state check in the same shape as
// ErrIncidentalAlreadyClosed's own, not a second write path - PostTransaction
// still writes exactly one row either way. The way back is
// Ledger.ReopenIncidental, not a bypass of this check.
var ErrIncidentalClosed = errors.New("ledger: incidental is closed")

// ErrIncidentalNotClosed is returned by ReopenIncidental when the envelope's
// closed_on is already NULL - there is nothing to reopen.
var ErrIncidentalNotClosed = errors.New("ledger: incidental is not closed")

// ErrIncidentalAlreadyClosed is returned by CloseIncidentalAndRoll when the
// envelope's closed_on is already set.
//
// Unlike ErrReimbursementAlreadySettled and ErrDuesPaymentAlreadyReversed, this is
// not a pre-check ahead of a unique index the schema already enforces:
// incidental carries no immutability trigger, and closing it is a plain
// UPDATE (ADR-024). Nothing in the schema stops a second UPDATE. This check
// is therefore the entire guarantee, not a defense-in-depth belt-and-braces
// on top of one: without it, a second call would roll again, quietly moving
// money out of an envelope the treasurer already considers settled and
// reported.
//
// PostTransaction's own ErrIncidentalClosed (ADR-031) is what now keeps a
// stray contribution from landing against a closed envelope's purpose_id in
// the first place - the risk this comment used to name as a reason nothing
// guards a second roll either. It no longer applies: ADR-027's "the purpose
// it tags stays open to new postings even after closed_on is set" is one of
// the two points ADR-031 supersedes.
var ErrIncidentalAlreadyClosed = errors.New("ledger: incidental has already been closed")
