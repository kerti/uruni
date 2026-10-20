package ledger

import "errors"

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
// guarantee, under any caller. The pre-check only turns it into a named error
// instead of a raw "UNIQUE constraint failed" (ADR-027). Under ADR-004's
// SetMaxOpenConns(1) a race between pre-check and insert is impossible, so
// this is not a lock.
var ErrReimbursementAlreadySettled = errors.New("ledger: reimbursement has already been settled")

// ErrFundAlreadyExists is returned by SetUpFund when a fund already exists.
//
// Unlike ErrReimbursementAlreadySettled and ErrDuesPaymentAlreadyReversed,
// no unique index backs this: "at most one fund" is application policy, not a
// schema fact (PRD section 6 keeps multiple funds open at the model level,
// and a schema constraint would need a migration to lift). The pre-check in
// SetUpFund's withTx is the entire guarantee, and it has no race behind it:
// SetMaxOpenConns(1) rules out two writers in this process, and #62's
// single-instance lock rules out a second `uruni serve` on the same file.
var ErrFundAlreadyExists = errors.New("ledger: a fund already exists")

// ErrDuesPaymentNotFound is returned by ReverseDuesPayment when
// GetTransactionForFund finds no row for the given fund and transaction id.
//
// The fetch is fund-scoped (WHERE fund_id = ? AND id = ?), so a real
// transaction id belonging to another fund answers with this same sentinel
// (ADR-029). That is what makes the composite FK on reverses_transaction_id
// load-bearing: a treasurer of one fund cannot even name another fund's row.
var ErrDuesPaymentNotFound = errors.New("ledger: no such transaction")

// ErrNotADuesPayment is returned by ReverseDuesPayment when the fetched row's
// Kind is not "dues".
//
// This also rules out reversing a reversal: a reversal is posted as
// kind='adjustment' (ADR-029), so it fails this same check.
var ErrNotADuesPayment = errors.New("ledger: transaction is not a dues payment")

// ErrDuesPaymentAlreadyReversed is returned by ReverseDuesPayment when
// GetDuesPaymentReversal finds an existing reversal row for the payment.
//
// The schema's dues_payment_reversed_once partial unique index is the actual
// guarantee, under any caller. This pre-check only turns it into a named
// error instead of a raw "UNIQUE constraint failed" (ADR-027); see
// ErrReimbursementAlreadySettled for why it is not a race.
var ErrDuesPaymentAlreadyReversed = errors.New("ledger: dues payment has already been reversed")

// ErrIncidentalClosed is returned by PostTransaction when PurposeID names an
// incidental whose closed_on is set (ADR-031).
//
// This supersedes ADR-027's accepted consequence that the purpose stays open
// to new postings after closing. It is symmetric: a late bill deserves
// attribution to the occasion as much as a late contribution. It is a
// read-before-write check, not a second write path. The way back is
// Ledger.ReopenIncidental, not a bypass.
var ErrIncidentalClosed = errors.New("ledger: incidental is closed")

// ErrContributionRequiresIncidentalPurpose is returned by PostTransaction
// when MemberID is set on a kind='normal' posting whose PurposeID does not
// name an incidental (ADR-034). It gives the caller a named error instead of
// the BEFORE INSERT trigger's raw message; the trigger is the real guarantee
// for anything that writes around the ledger.
var ErrContributionRequiresIncidentalPurpose = errors.New("ledger: a named contribution must be tagged to an incidental purpose")

// ErrIncidentalNotClosed is returned by ReopenIncidental when the envelope's
// closed_on is already NULL - there is nothing to reopen.
var ErrIncidentalNotClosed = errors.New("ledger: incidental is not closed")

// ErrIncidentalAlreadyClosed is returned by CloseIncidentalAndRoll when the
// envelope's closed_on is already set.
//
// Unlike ErrReimbursementAlreadySettled and ErrDuesPaymentAlreadyReversed,
// no unique index backs this: incidental has no immutability trigger and
// closing is a plain UPDATE (ADR-024), so this check is the entire
// guarantee. Without it a second call would roll again, moving money out of
// an envelope the treasurer already considers settled and reported.
//
// ErrIncidentalClosed (ADR-031) keeps a stray contribution from landing
// against a closed envelope's purpose_id, which ADR-031 supersedes ADR-027
// on.
var ErrIncidentalAlreadyClosed = errors.New("ledger: incidental has already been closed")

// ErrPurposeCorrectionNotFound is returned by PostPurposeCorrection when
// GetTransactionForFund finds no row for the fund and transaction id,
// including a real id of another fund (fund-scoped like ErrDuesPaymentNotFound,
// ADR-029 applied to ADR-033).
var ErrPurposeCorrectionNotFound = errors.New("ledger: no such transaction")

// ErrPurposeCorrectionOpening is returned by PostPurposeCorrection when the
// row being corrected is kind='opening'. Its peruntukan is Kas Utama by
// construction (mainPurposeID, ADR-024) - there is no wrong tag to fix.
var ErrPurposeCorrectionOpening = errors.New("ledger: cannot correct the peruntukan of an opening balance")

// ErrPurposeCorrectionDues is returned by PostPurposeCorrection when the row
// being corrected is kind='dues'. A tag bound to the dues concept is not a
// mis-tag to fix but a different entry (ADR-033).
var ErrPurposeCorrectionDues = errors.New("ledger: cannot correct the peruntukan of a dues payment")

// ErrPurposeCorrectionReimbursement is returned by PostPurposeCorrection
// when the row being corrected is kind='reimbursement'. Its payout inherits
// the claim's peruntukan, and the correction already exists as the claim edit
// (UpdateReimbursement, Talangan's "Perbaiki"); ADR-033 points there.
var ErrPurposeCorrectionReimbursement = errors.New("ledger: cannot correct the peruntukan of a reimbursement payout - use Talangan's Perbaiki instead")

// ErrPurposeCorrectionTransfer is returned by PostPurposeCorrection when the
// row being corrected is kind='transfer'. A leg is half a movement, and
// correcting one alone would break the pair's value-neutrality (ADR-033).
// Since a correction's legs are kind='transfer', this also makes a correction
// of a correction impossible, with no depth limit or cycle rule.
var ErrPurposeCorrectionTransfer = errors.New("ledger: cannot correct the peruntukan of a transfer leg")

// ErrPurposeCorrectionDuesReversal is returned by PostPurposeCorrection when
// the row being corrected is kind='adjustment' with reverses_transaction_id
// set - a dues reversal (ADR-029), not the reconciliation-fix adjustment
// ADR-033 opens this route for.
var ErrPurposeCorrectionDuesReversal = errors.New("ledger: cannot correct the peruntukan of a dues reversal")

// ErrPurposeCorrectionNoop is returned by PostPurposeCorrection when the
// requested purpose_id is exactly the row's current effective peruntukan -
// nothing would move, so nothing is posted (ADR-033).
var ErrPurposeCorrectionNoop = errors.New("ledger: the requested purpose_id is already this row's peruntukan")

// ErrPurposeCorrectionNamedContribution is returned by PostPurposeCorrection
// when the row being corrected is kind='normal' and carries a member_id - a
// named contribution (ADR-034). Moving it would leave participation counting
// the member against an envelope the money has left, so it is refused like a
// dues payment; the fix is reverse and post again. An unnamed contribution
// keeps ADR-033.
var ErrPurposeCorrectionNamedContribution = errors.New("ledger: cannot correct the peruntukan of a named contribution - reverse and post again instead")

// ErrPurposeCorrectionTargetClosed is returned by PostPurposeCorrection when
// the requested purpose_id names a closed incidental. Correcting into a
// closed envelope would post to a purpose ADR-031 refuses new postings
// against; the way back is Ledger.ReopenIncidental (ADR-033).
var ErrPurposeCorrectionTargetClosed = errors.New("ledger: cannot correct into a closed incidental - reopen it first")

// ErrPurposeCorrectionSourceClosed is returned by PostPurposeCorrection when
// the row's effective peruntukan names a closed incidental. Correcting out
// would leave its balance non-zero, breaking ADR-031's rollover invariant
// (#270); the way back is Ledger.ReopenIncidental (ADR-033).
var ErrPurposeCorrectionSourceClosed = errors.New("ledger: cannot correct out of a closed incidental - reopen it first")

// ErrPurposeMoveSamePurpose is returned by PostPurposeMove when the source
// and the target are the same purpose: nothing would move (ADR-036).
var ErrPurposeMoveSamePurpose = errors.New("ledger: a purpose move needs two different purposes")

// ErrPurposeMoveUnknownPurpose is returned by PostPurposeMove when either
// purpose is not this fund's - an id naming nothing, or another fund's. The
// lookup is fund-scoped, so both answer the same (ADR-029).
var ErrPurposeMoveUnknownPurpose = errors.New("ledger: no such purpose in this fund")

// ErrPurposeMovePassThrough is returned by PostPurposeMove when either side
// is a pass-through purpose (Titipan). That money belongs to the parent body
// and leaves only by being forwarded; moving it would spend money that is
// not the fund's (ADR-036).
var ErrPurposeMovePassThrough = errors.New("ledger: pass-through money cannot be moved between purposes")

// ErrPurposeMoveClosed is returned by PostPurposeMove when either side is a
// closed incidental. The way back is Ledger.ReopenIncidental (ADR-031,
// ADR-036).
var ErrPurposeMoveClosed = errors.New("ledger: cannot move money into or out of a closed incidental - reopen it first")

// ErrPurposeMoveInsufficient is returned by PostPurposeMove when the amount
// exceeds the source purpose's balance at the moment of posting: a purpose
// cannot give more than it holds (ADR-036).
var ErrPurposeMoveInsufficient = errors.New("ledger: the source purpose holds less than the amount")

// ErrPurposeMoveUnknownAccount is returned by PostPurposeMove when the
// account is not this fund's - an id naming nothing, or another fund's.
var ErrPurposeMoveUnknownAccount = errors.New("ledger: no such account in this fund")

// ErrPurposeMoveAccountInactive is returned by PostPurposeMove when the
// account is retired (inactive_on set), as the record forms already apply.
var ErrPurposeMoveAccountInactive = errors.New("ledger: the account is inactive")

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
// correct - ReverseDuesPayment and PostPurposeCorrection: the treasurer
// chooses nothing there, and refusing would make a mistake on a
// since-retired location uncorrectable (CLAUDE.md rule 3).
var ErrAccountInactive = errors.New("ledger: the account is retired")

// ErrAccountHoldsMoney is returned by UpdateAccount when asked to retire a
// location whose recorded balance is above zero (#474): the money has to be
// moved out first (Pindah lokasi).
var ErrAccountHoldsMoney = errors.New("ledger: the account still holds money")

// ErrAccountBalanceNegative is UpdateAccount's refusal for a location whose
// recorded balance is below zero (#474). There is nothing to move (more left
// than came in), so the next step is a Cek kas that finds the gap.
var ErrAccountBalanceNegative = errors.New("ledger: the account's recorded balance is negative")

// ErrReconciliationMissingLocation is returned by TakeReconciliation when
// the snapshot leaves out an active location: Cek kas counts each active
// location, never a subset (PRD section 7.8), or it could freeze a "cocok"
// snapshot while an uncounted location held the discrepancy. Nothing is
// written.
var ErrReconciliationMissingLocation = errors.New("ledger: the count omits an active location")

// ErrReconciliationPlugMismatch is returned by TakeReconciliation when a line
// resolved as "adjusted" carries a fix whose signed amount (+ for in, - for
// out) is not exactly the line's difference (#498). An adjusted line is a
// plug that claims the gap is squared; a fix that undershoots, overshoots or
// points the wrong way would let Beranda and the public report say cocok
// while a gap remains. Nothing is written. A forgotten entry that explains
// part of a gap is "entry_added", which keeps the remainder open.
var ErrReconciliationPlugMismatch = errors.New("ledger: an adjusted fix must equal the line's difference")

// ErrDuesRateSuperseded is returned by CorrectDuesRate and DeleteDuesRate when the
// row named is not the one that stands for its month (ADR-040): a correction is a
// new row with a new id, so a page that still holds the old id is stale, and
// applying its edit to the successor would overwrite a figure the caller has not
// seen. A row that is itself a deleting correction counts: the month has no rate
// to correct or delete. Nothing is written; the way forward is to reload the
// list.
var ErrDuesRateSuperseded = errors.New("ledger: the dues rate no longer stands")

// ErrDuesRateMonthTaken is returned by CreateDuesRate when the tier already has a
// rate standing for the month. Adding a rate into a month never replaces what it
// says (that is PATCH, a correction); only a month whose standing row is a
// deletion takes a new one. The wire keeps the 409 unique_violation it has
// always answered.
var ErrDuesRateMonthTaken = errors.New("ledger: the tier already has a rate for that month")
