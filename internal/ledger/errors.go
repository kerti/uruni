package ledger

import "fmt"

// Class is how a refusal reads to a caller, independent of any transport: the
// HTTP layer turns it into a status in one place, and the ledger stays free of
// net/http.
type Class int

const (
	// ClassInvalid marks a refusal where the request itself is at fault: a field
	// that is malformed, out of range or names nothing in this fund. Changing the
	// request fixes it.
	ClassInvalid Class = iota + 1
	// ClassNotFound marks an id that names nothing in this fund, another fund's
	// included (ADR-029).
	ClassNotFound
	// ClassConflict marks a well-formed request the ledger's state refuses.
	// Changing the state (reopening, reconciling, moving money) or reloading is the
	// way through.
	ClassConflict
)

// Error is a refusal the ledger names: one value per business-state failure,
// carrying the wire code and the English message a client is answered with, so
// defining a refusal is the only place it is declared (ADR-027's business-state
// sentinels, each its own named error and code). Callers branch with errors.Is on the exported sentinel;
// wrapping with %w to add context keeps both that and the wire answer.
//
// Error() is the log text. The message is what goes on the wire: fixed, in
// English, and never an amount or a date (ADR-022).
type Error struct {
	text    string
	class   Class
	code    string
	message string
}

func (e *Error) Error() string { return e.text }

// Code is the snake_case slug the error envelope carries. Several refusals may
// share one when the client's answer to them is the same (not_found).
func (e *Error) Code() string { return e.code }

// Message is the envelope's English sentence.
func (e *Error) Message() string { return e.message }

// Class is the refusal's kind.
func (e *Error) Class() Class { return e.class }

// refusals lists every Error refuse has made, in definition order, for Errors.
var refusals []*Error

// refuse defines a domain error. Package-level only: it appends to refusals
// during initialization.
func refuse(text string, class Class, code, message string) *Error {
	e := &Error{text: text, class: class, code: code, message: message}
	refusals = append(refusals, e)
	return e
}

// Errors returns every domain error this package defines, so a contract test can
// check them all without a second list to keep in step with the definitions.
func Errors() []*Error { return append([]*Error(nil), refusals...) }

// ErrInvalidArgument is the fourth error category ADR-027 adds to the three
// this package's writes otherwise produce (money.ErrOverflow, a raw database
// error, and the business-state sentinels): the caller's input failed a
// shape check before the write reached the schema - a non-positive amount, an
// occurred_on that is not a real calendar date, an empty required field.
//
// Every returned error wraps this with %w and names the field, so callers use
// errors.Is rather than matching a string, and the HTTP layer maps it to 400.
// It also keeps this package safe to call outside an HTTP handler (ADR-012's
// import has no request-validation layer).
//
// Not for composite-FK violations, an account of another fund, an unknown
// id - anything the schema already enforces. Those are domain bugs, not
// caller mistakes, and surface wrapped generically as a 500. Re-deriving a
// cross-row invariant here would be a second source of truth (ADR-027).
var ErrInvalidArgument = refuse("ledger: invalid argument",
	ClassInvalid, "invalid_argument",
	"The request contains an invalid argument.")

// FieldError is an ErrInvalidArgument that one request field is at fault for
// (#530). Field is that field's JSON key, dotted for a nested one and without a
// list position ("counts.fix.amount"; Reason says which item), so the HTTP
// layer can hand it to the client as the error envelope's `field`. Its text is
// what the %w form gave before it existed: "ledger: invalid argument: <reason>".
//
// A check that no single field fails (two fields that must differ, a rule
// across rows) still wraps ErrInvalidArgument plainly, or names the field the
// client would change.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return ErrInvalidArgument.Error() + ": " + e.Reason }

func (e *FieldError) Unwrap() error { return ErrInvalidArgument }

// InvalidField is how internal/http reads the field without importing this
// type: auth's FieldError answers the same method.
func (e *FieldError) InvalidField() string { return e.Field }

// invalidField builds a FieldError; format and args are the reason.
func invalidField(field, format string, args ...any) error {
	return &FieldError{Field: field, Reason: fmt.Sprintf(format, args...)}
}

// ErrReimbursementWaived is returned by SettleReimbursement when the claim's
// waived_on is set: a claim the treasurer has already written off as never
// going to be repaid cannot also be settled.
var ErrReimbursementWaived = refuse("ledger: reimbursement has been waived",
	ClassConflict, "reimbursement_waived",
	"This reimbursement has been waived.")

// ErrReimbursementAlreadySettled is returned by SettleReimbursement when
// GetReimbursementSettlement finds an existing kind='reimbursement' row for
// the claim.
//
// The schema's reimbursement_settled_once partial unique index is the actual
// guarantee, under any caller. The pre-check only turns it into a named error
// instead of a raw "UNIQUE constraint failed" (ADR-027). Under ADR-004's
// SetMaxOpenConns(1) a race between pre-check and insert is impossible, so
// this is not a lock.
var ErrReimbursementAlreadySettled = refuse("ledger: reimbursement has already been settled",
	ClassConflict, "reimbursement_already_settled",
	"This reimbursement has already been settled.")

// ErrFundAlreadyExists is returned by SetUpFund when a fund already exists.
//
// Unlike ErrReimbursementAlreadySettled and ErrAlreadyReversed,
// no unique index backs this: "at most one fund" is application policy, not a
// schema fact (PRD section 6 keeps multiple funds open at the model level,
// and a schema constraint would need a migration to lift). The pre-check in
// SetUpFund's withTx is the entire guarantee, and it has no race behind it:
// SetMaxOpenConns(1) rules out two writers in this process, and #62's
// single-instance lock rules out a second `uruni serve` on the same file.
var ErrFundAlreadyExists = refuse("ledger: a fund already exists",
	ClassConflict, "fund_already_exists",
	"A fund has already been set up.")

// The reversal refusals (ADR-042): one error and one wire code each, never a
// blanket rule (ADR-033), each a pre-check ahead of a schema guarantee.

// ErrReversalNotFound is returned by ReverseTransaction when GetReversalTarget
// finds no row for the given fund and transaction id. The fetch is
// fund-scoped, so another fund's id answers with this same sentinel (ADR-029).
var ErrReversalNotFound = refuse("ledger: no such transaction",
	ClassNotFound, "not_found",
	"The requested resource was not found.")

// ErrReversalOpening is returned by ReverseTransaction for a kind='opening'
// row: it is the Saldo awal line and opening_balance_once_per_account.
var ErrReversalOpening = refuse("ledger: an opening balance cannot be reversed",
	ClassConflict, "reversal_opening",
	"An opening balance cannot be reversed.")

// ErrReversalReimbursement is returned by ReverseTransaction for a
// kind='reimbursement' payout: it is a claim's settled state
// (reimbursement_settled_once), and a claim is corrected before it is settled.
var ErrReversalReimbursement = refuse("ledger: a reimbursement payout cannot be reversed",
	ClassConflict, "reversal_reimbursement",
	"A reimbursement payout cannot be reversed.")

// ErrReversalTransfer is returned by ReverseTransaction for a kind='transfer'
// leg: it is half of a value-neutral pair.
var ErrReversalTransfer = refuse("ledger: a transfer leg cannot be reversed",
	ClassConflict, "reversal_transfer",
	"A transfer leg cannot be reversed.")

// ErrReversalOfReversal is returned by ReverseTransaction for a
// kind='adjustment' row that itself reverses another.
var ErrReversalOfReversal = refuse("ledger: a reversal cannot be reversed",
	ClassConflict, "reversal_of_reversal",
	"A reversal cannot be reversed.")

// ErrReversalAdjustment is returned by ReverseTransaction for any other
// kind='adjustment' row: the plug a cek kas line names, or one posted alone.
var ErrReversalAdjustment = refuse("ledger: an adjustment cannot be reversed",
	ClassConflict, "reversal_adjustment",
	"An adjustment cannot be reversed.")

// ErrReversalReconciliationFix is returned by ReverseTransaction for a
// kind='normal' row a cek kas line names as its fix (resolution entry_added).
// The line's open remainder is derived through that entry in four queries,
// none of which reads a reversal, so reversing it would leave the line saying
// an entry the ledger says never happened explains the gap. The way is the
// hand-made opposite entry and a new count (ADR-042).
var ErrReversalReconciliationFix = refuse("ledger: a row a cek kas line names as its fix cannot be reversed",
	ClassConflict, "reversal_reconciliation_fix",
	"An entry a cash count names as its fix cannot be reversed.")

// ErrAlreadyReversed is returned by ReverseTransaction when ledger_entry says
// something already reverses the row.
//
// The schema's dues_payment_reversed_once partial unique index is the actual
// guarantee, under any caller. This pre-check only turns it into a named
// error instead of a raw "UNIQUE constraint failed" (ADR-027); see
// ErrReimbursementAlreadySettled for why it is not a race.
var ErrAlreadyReversed = refuse("ledger: transaction has already been reversed",
	ClassConflict, "transaction_already_reversed",
	"This transaction has already been reversed.")

// ErrReversalBeforeOriginal is returned by ReverseTransaction when a cek kas
// has counted the row and it is dated after today, so the day the reversal
// would take (today) precedes what it reverses. Only a row from before the date
// ceiling can be there. The hand-made opposite entry is the way out (ADR-042);
// the schema does not check this, so a restore is not refused.
var ErrReversalBeforeOriginal = refuse("ledger: a reversal cannot come before the row it reverses",
	ClassConflict, "reversal_before_original",
	"A cash count has covered this transaction and it is dated after today, so its reversal would come before it.")

// ErrIncidentalClosed is returned by PostTransaction when PurposeID names an
// incidental whose closed_on is set (ADR-031).
//
// This supersedes ADR-027's accepted consequence that the purpose stays open
// to new postings after closing. It is symmetric: a late bill deserves
// attribution to the occasion as much as a late contribution. It is a
// read-before-write check, not a second write path. The way back is
// Ledger.ReopenIncidental, not a bypass.
var ErrIncidentalClosed = refuse("ledger: incidental is closed",
	ClassConflict, "incidental_closed",
	"This incidental is closed and cannot accept new transactions.")

// ErrContributionRequiresIncidentalPurpose is returned by PostTransaction
// when MemberID is set on a kind='normal' posting whose PurposeID does not
// name an incidental (ADR-034). It gives the caller a named error instead of
// the BEFORE INSERT trigger's raw message; the trigger is the real guarantee
// for anything that writes around the ledger.
var ErrContributionRequiresIncidentalPurpose = refuse("ledger: a named contribution must be tagged to an incidental purpose",
	ClassInvalid, "invalid_argument",
	"A named contribution must be tagged to an incidental (envelope) purpose.")

// ErrIncidentalNotClosed is returned by ReopenIncidental when the envelope's
// closed_on is already NULL - there is nothing to reopen.
var ErrIncidentalNotClosed = refuse("ledger: incidental is not closed",
	ClassConflict, "incidental_not_closed",
	"This incidental is not closed.")

// ErrIncidentalAlreadyClosed is returned by CloseIncidentalAndRoll when the
// envelope's closed_on is already set.
//
// Unlike ErrReimbursementAlreadySettled and ErrAlreadyReversed,
// no unique index backs this: incidental has no immutability trigger and
// closing is a plain UPDATE (ADR-024), so this check is the entire
// guarantee. Without it a second call would roll again, moving money out of
// an envelope the treasurer already considers settled and reported.
//
// ErrIncidentalClosed (ADR-031) keeps a stray contribution from landing
// against a closed envelope's purpose_id, which ADR-031 supersedes ADR-027
// on.
var ErrIncidentalAlreadyClosed = refuse("ledger: incidental has already been closed",
	ClassConflict, "incidental_already_closed",
	"This incidental has already been closed.")

// ErrPurposeCorrectionNotFound is returned by PostPurposeCorrection when
// GetTransactionForFund finds no row for the fund and transaction id,
// including a real id of another fund (fund-scoped like ErrReversalNotFound,
// ADR-029 applied to ADR-033).
var ErrPurposeCorrectionNotFound = refuse("ledger: no such transaction",
	ClassNotFound, "not_found",
	"The requested resource was not found.")

// ErrPurposeCorrectionOpening is returned by PostPurposeCorrection when the
// row being corrected is kind='opening'. Its peruntukan is Kas Utama by
// construction (mainPurposeID, ADR-024) - there is no wrong tag to fix.
var ErrPurposeCorrectionOpening = refuse("ledger: cannot correct the peruntukan of an opening balance",
	ClassConflict, "purpose_correction_opening",
	"An opening balance's purpose cannot be corrected.")

// ErrPurposeCorrectionDues is returned by PostPurposeCorrection when the row
// being corrected is kind='dues'. A tag bound to the dues concept is not a
// mis-tag to fix but a different entry (ADR-033).
var ErrPurposeCorrectionDues = refuse("ledger: cannot correct the peruntukan of a dues payment",
	ClassConflict, "purpose_correction_dues",
	"A dues payment's purpose cannot be corrected.")

// ErrPurposeCorrectionReimbursement is returned by PostPurposeCorrection
// when the row being corrected is kind='reimbursement'. Its payout inherits
// the claim's peruntukan, and the correction already exists as the claim edit
// (UpdateReimbursement, Talangan's "Perbaiki"); ADR-033 points there.
var ErrPurposeCorrectionReimbursement = refuse("ledger: cannot correct the peruntukan of a reimbursement payout - use Talangan's Perbaiki instead",
	ClassConflict, "purpose_correction_reimbursement",
	"A reimbursement payout's purpose cannot be corrected here - correct the claim itself instead.")

// ErrPurposeCorrectionTransfer is returned by PostPurposeCorrection when the
// row being corrected is kind='transfer'. A leg is half a movement, and
// correcting one alone would break the pair's value-neutrality (ADR-033).
// Since a correction's legs are kind='transfer', this also makes a correction
// of a correction impossible, with no depth limit or cycle rule.
var ErrPurposeCorrectionTransfer = refuse("ledger: cannot correct the peruntukan of a transfer leg",
	ClassConflict, "purpose_correction_transfer",
	"A transfer leg's purpose cannot be corrected.")

// ErrPurposeCorrectionReversal is returned by PostPurposeCorrection when the
// row being corrected is kind='adjustment' with reverses_transaction_id set -
// a reversal of any row (ADR-029, ADR-042), not the reconciliation-fix
// adjustment ADR-033 opens this route for.
var ErrPurposeCorrectionReversal = refuse("ledger: cannot correct the peruntukan of a reversal",
	ClassConflict, "purpose_correction_reversal",
	"A reversal's purpose cannot be corrected.")

// ErrPurposeCorrectionAlreadyReversed is returned by PostPurposeCorrection when
// the row being corrected has been reversed (ADR-042). The reversal already took
// the row's money out of its effective purpose; moving the original afterwards
// would put the pair's legs on a purpose that no longer holds it and leave the
// reversal netting against the wrong one. Correct-then-reverse is allowed (the
// reversal copies the effective purpose); this is the other order, reachable
// from a stale page.
var ErrPurposeCorrectionAlreadyReversed = refuse("ledger: cannot correct the peruntukan of a reversed transaction",
	ClassConflict, "purpose_correction_already_reversed",
	"A reversed transaction's purpose cannot be corrected.")

// ErrPurposeCorrectionNoop is returned by PostPurposeCorrection when the
// requested purpose_id is exactly the row's current effective peruntukan -
// nothing would move, so nothing is posted (ADR-033).
var ErrPurposeCorrectionNoop = refuse("ledger: the requested purpose_id is already this row's peruntukan",
	ClassConflict, "purpose_correction_noop",
	"This row is already tagged to that purpose.")

// ErrPurposeCorrectionNamedContribution is returned by PostPurposeCorrection
// when the row being corrected is kind='normal' and carries a member_id - a
// named contribution (ADR-034). Moving it would leave participation counting
// the member against an envelope the money has left, so it is refused like a
// dues payment; the fix is reverse and post again. An unnamed contribution
// keeps ADR-033.
var ErrPurposeCorrectionNamedContribution = refuse("ledger: cannot correct the peruntukan of a named contribution - reverse and post again instead",
	ClassConflict, "purpose_correction_named_contribution",
	"A named contribution's purpose cannot be corrected - reverse it and post again instead.")

// ErrPurposeCorrectionTargetClosed is returned by PostPurposeCorrection when
// the requested purpose_id names a closed incidental. Correcting into a
// closed envelope would post to a purpose ADR-031 refuses new postings
// against; the way back is Ledger.ReopenIncidental (ADR-033).
var ErrPurposeCorrectionTargetClosed = refuse("ledger: cannot correct into a closed incidental - reopen it first",
	ClassConflict, "purpose_correction_target_closed",
	"This incidental is closed - reopen it before correcting into it.")

// ErrPurposeCorrectionSourceClosed is returned by PostPurposeCorrection when
// the row's effective peruntukan names a closed incidental. Correcting out
// would leave its balance non-zero, breaking ADR-031's rollover invariant
// (#270); the way back is Ledger.ReopenIncidental (ADR-033).
var ErrPurposeCorrectionSourceClosed = refuse("ledger: cannot correct out of a closed incidental - reopen it first",
	ClassConflict, "purpose_correction_source_closed",
	"This incidental is closed - reopen it before correcting out of it.")

// ErrPurposeMoveSamePurpose is returned by PostPurposeMove when the source
// and the target are the same purpose: nothing would move (ADR-036).
var ErrPurposeMoveSamePurpose = refuse("ledger: a purpose move needs two different purposes",
	ClassInvalid, "purpose_move_same_purpose",
	"A purpose move needs two different purposes.")

// ErrPurposeMoveUnknownPurpose is returned by PostPurposeMove when either
// purpose is not this fund's - an id naming nothing, or another fund's. The
// lookup is fund-scoped, so both answer the same (ADR-029).
var ErrPurposeMoveUnknownPurpose = refuse("ledger: no such purpose in this fund",
	ClassInvalid, "invalid_argument",
	"The request references a purpose that does not exist.")

// ErrPurposeMovePassThrough is returned by PostPurposeMove when either side
// is a pass-through purpose (Titipan). That money belongs to the parent body
// and leaves only by being forwarded; moving it would spend money that is
// not the fund's (ADR-036).
var ErrPurposeMovePassThrough = refuse("ledger: pass-through money cannot be moved between purposes",
	ClassConflict, "purpose_move_pass_through",
	"Pass-through money cannot be moved between purposes.")

// ErrPurposeMoveClosed is returned by PostPurposeMove when either side is a
// closed incidental. The way back is Ledger.ReopenIncidental (ADR-031,
// ADR-036).
var ErrPurposeMoveClosed = refuse("ledger: cannot move money into or out of a closed incidental - reopen it first",
	ClassConflict, "purpose_move_closed",
	"This incidental is closed - reopen it before moving money into or out of it.")

// ErrPurposeMoveInsufficient is returned by PostPurposeMove when the amount
// exceeds the source purpose's balance at the moment of posting: a purpose
// cannot give more than it holds (ADR-036).
var ErrPurposeMoveInsufficient = refuse("ledger: the source purpose holds less than the amount",
	ClassConflict, "purpose_move_insufficient",
	"The source purpose holds less than that amount.")

// ErrPurposeMoveUnknownAccount is returned by PostPurposeMove when the
// account is not this fund's - an id naming nothing, or another fund's.
var ErrPurposeMoveUnknownAccount = refuse("ledger: no such account in this fund",
	ClassInvalid, "invalid_argument",
	"The request references an account that does not exist.")

// ErrPurposeMoveAccountInactive is returned by PostPurposeMove when the
// account is retired (inactive_on set), as the record forms already apply.
var ErrPurposeMoveAccountInactive = refuse("ledger: the account is inactive",
	ClassConflict, "purpose_move_account_inactive",
	"This account is inactive.")

// ErrAccountInactive is returned by every posting that names a location the
// caller chose - PostTransaction, PostDuesPayments,
// PostTransferBetweenAccounts (either side), SettleReimbursement and
// CloseIncidentalAndRoll when it has something to roll - and by
// TakeReconciliation when a count names one, once that location is retired
// (account.inactive_on non-NULL, whatever date it holds, PRD section 6 and
// 7.8). Nothing is written. Reinstating the location (PATCH inactive_on null)
// is the way back.
//
// PostPurposeMove keeps ErrPurposeMoveAccountInactive and its wire code: that
// API is shipped and the SPA speaks it.
//
// Not applied to postings that inherit their location from the row they
// correct - ReverseTransaction and PostPurposeCorrection: the treasurer
// chooses nothing there, and refusing would make a mistake on a
// since-retired location uncorrectable (CLAUDE.md rule 3).
var ErrAccountInactive = refuse("ledger: the account is retired",
	ClassConflict, "account_inactive",
	"This location has been retired and cannot take new entries.")

// ErrAccountHoldsMoney is returned by UpdateAccount when asked to retire a
// location whose recorded balance is above zero (#474): the money has to be
// moved out first (Pindah lokasi).
var ErrAccountHoldsMoney = refuse("ledger: the account still holds money",
	ClassConflict, "account_holds_money",
	"This location still holds money; move it out before retiring the location.")

// ErrAccountBalanceNegative is UpdateAccount's refusal for a location whose
// recorded balance is below zero (#474). There is nothing to move (more left
// than came in), so the next step is a Cek kas that finds the gap.
var ErrAccountBalanceNegative = refuse("ledger: the account's recorded balance is negative",
	ClassConflict, "account_balance_negative",
	"This location's recorded balance is negative; reconcile it before retiring the location.")

// ErrReconciliationMissingLocation is returned by TakeReconciliation when
// the snapshot leaves out an active location: Cek kas counts each active
// location, never a subset (PRD section 7.8), or it could freeze a "cocok"
// snapshot while an uncounted location held the discrepancy. Nothing is
// written.
var ErrReconciliationMissingLocation = refuse("ledger: the count omits an active location",
	ClassConflict, "reconciliation_location_missing",
	"A cash count must include every active location.")

// ErrReconciliationPlugMismatch is returned by TakeReconciliation when a line
// resolved as "adjusted" carries a fix whose signed amount (+ for in, - for
// out) is not exactly the line's difference (#498). An adjusted line is a
// plug that claims the gap is squared; a fix that undershoots, overshoots or
// points the wrong way would let Beranda and the public report say cocok
// while a gap remains. Nothing is written. A forgotten entry that explains
// part of a gap is "entry_added", which keeps the remainder open.
var ErrReconciliationPlugMismatch = refuse("ledger: an adjusted fix must equal the line's difference",
	ClassConflict, "reconciliation_plug_mismatch",
	"An adjusting entry must equal the difference exactly.")

// ErrDuesRateSuperseded is returned by CorrectDuesRate and DeleteDuesRate when the
// row named is not the one that stands for its month (ADR-040): a correction is a
// new row with a new id, so a page holding the old id is stale, and applying its
// edit to the successor would overwrite a figure the caller has not seen. A
// deleting correction counts too. Nothing is written.
var ErrDuesRateSuperseded = refuse("ledger: the dues rate no longer stands",
	ClassConflict, "dues_rate_superseded",
	"This rate has since been corrected or removed; reload the list.")

// ErrDuesRateMonthTaken is returned by CreateDuesRate when the tier already has a
// rate standing for the month. Adding never replaces (that is a correction);
// only a month whose standing row is a deletion takes a new one.
var ErrDuesRateMonthTaken = refuse("ledger: the tier already has a rate for that month",
	ClassConflict, "unique_violation",
	"This value conflicts with an existing record.")

// ErrIdempotencyKeyReused is returned by a keyed write when its idempotency key
// is on a live record made for another request (ADR-043): the same key with a
// different fingerprint. Nothing is written. A replay is a success, not an error.
var ErrIdempotencyKeyReused = refuse("ledger: the idempotency key was used for another request",
	ClassConflict, "idempotency_key_reused",
	"This Idempotency-Key was already used for a different request.")
