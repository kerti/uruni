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

// ErrContributionRequiresIncidentalPurpose is returned by PostTransaction
// when MemberID is set on a kind='normal' posting whose PurposeID does not
// name an incidental (ADR-034). It exists purely to give the caller a
// clean, named error instead of the BEFORE INSERT trigger's raw SQLite
// message - the trigger is what makes a named row unrepresentable for
// anything that writes around the ledger, this pre-check is what keeps the
// treasurer's own mistake reading as copy rather than a constraint string
// (the same "check first, let the schema be the real guarantee" shape
// ErrIncidentalClosed already uses).
var ErrContributionRequiresIncidentalPurpose = errors.New("ledger: a named contribution must be tagged to an incidental purpose")

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

// ErrPurposeCorrectionNotFound is returned by PostPurposeCorrection when
// GetTransactionForFund finds no row for the given fund and transaction id -
// including a real id that belongs to another fund, the same fund-scoped
// shape ErrDuesPaymentNotFound's own comment argues for (ADR-029, applied
// here to ADR-033).
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
// the claim's peruntukan, and its correction already exists as
// SettleReimbursement's own claim edit (UpdateReimbursement, Talangan's
// "Perbaiki") - ADR-033 names that as where to go rather than refusing
// flatly.
var ErrPurposeCorrectionReimbursement = errors.New("ledger: cannot correct the peruntukan of a reimbursement payout - use Talangan's Perbaiki instead")

// ErrPurposeCorrectionTransfer is returned by PostPurposeCorrection when the
// row being corrected is itself kind='transfer'. A leg is half a movement,
// and correcting one alone would break the pair's value-neutrality
// (ADR-033) - and since a correction's own two legs are posted as
// kind='transfer', this is also what makes a correction of a correction
// structurally impossible, with no depth limit or cycle rule needed.
var ErrPurposeCorrectionTransfer = errors.New("ledger: cannot correct the peruntukan of a transfer leg")

// ErrPurposeCorrectionDuesReversal is returned by PostPurposeCorrection when
// the row being corrected is kind='adjustment' with reverses_transaction_id
// set - a dues reversal (ADR-029), not the reconciliation-fix shape of
// adjustment ADR-033 opens this route for. Its own correction path is
// ReverseDuesPayment, not this one.
var ErrPurposeCorrectionDuesReversal = errors.New("ledger: cannot correct the peruntukan of a dues reversal")

// ErrPurposeCorrectionNoop is returned by PostPurposeCorrection when the
// requested purpose_id is exactly the row's current effective peruntukan -
// nothing would move, so nothing is posted (ADR-033).
var ErrPurposeCorrectionNoop = errors.New("ledger: the requested purpose_id is already this row's peruntukan")

// ErrPurposeCorrectionNamedContribution is returned by PostPurposeCorrection
// when the row being corrected is kind='normal' and carries a member_id - a
// named contribution (ADR-034). Moving it to another peruntukan would leave
// participation counting the member against an envelope the money has left,
// so it is refused the way a dues payment already is; the fix is reverse and
// post again. An unnamed contribution (member_id NULL) keeps ADR-033 exactly
// as it is.
var ErrPurposeCorrectionNamedContribution = errors.New("ledger: cannot correct the peruntukan of a named contribution - reverse and post again instead")

// ErrPurposeCorrectionTargetClosed is returned by PostPurposeCorrection when
// the requested purpose_id names a closed incidental. Correcting into a
// closed envelope would post to a purpose ADR-031 refuses new postings
// against; the way back is Ledger.ReopenIncidental (ADR-033).
var ErrPurposeCorrectionTargetClosed = errors.New("ledger: cannot correct into a closed incidental - reopen it first")

// ErrPurposeCorrectionSourceClosed is returned by PostPurposeCorrection when
// the row's effective peruntukan names a closed incidental. Correcting out
// of a closed envelope would leave its balance non-zero, breaking the
// rollover invariant ADR-031 established and #270's derived rollover reads;
// the way back is Ledger.ReopenIncidental (ADR-033).
var ErrPurposeCorrectionSourceClosed = errors.New("ledger: cannot correct out of a closed incidental - reopen it first")

// ErrPurposeMoveSamePurpose is returned by PostPurposeMove when the source
// and the target are the same purpose: nothing would move (ADR-036).
var ErrPurposeMoveSamePurpose = errors.New("ledger: a purpose move needs two different purposes")

// ErrPurposeMoveUnknownPurpose is returned by PostPurposeMove when either
// purpose is not one of this fund's - an id that names nothing, or another
// fund's purpose. The lookup is fund-scoped, so the two answer the same way
// (ADR-029's shape).
var ErrPurposeMoveUnknownPurpose = errors.New("ledger: no such purpose in this fund")

// ErrPurposeMovePassThrough is returned by PostPurposeMove when either side
// is a pass-through purpose (Titipan). That money belongs to the parent body
// and leaves only by being forwarded; moving it into Kas Utama or an
// envelope would spend money that is not the fund's (ADR-036).
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
// account is not one of this fund's - an id that names nothing, or another
// fund's account.
var ErrPurposeMoveUnknownAccount = errors.New("ledger: no such account in this fund")

// ErrPurposeMoveAccountInactive is returned by PostPurposeMove when the
// account has been retired (inactive_on set), the same rule the record forms
// apply by not offering it.
var ErrPurposeMoveAccountInactive = errors.New("ledger: the account is inactive")

// ErrAccountInactive is returned by every posting that names a location the
// caller chose - PostTransaction, PostDuesPayments,
// PostTransferBetweenAccounts (either side), SettleReimbursement and
// CloseIncidentalAndRoll when it has something to roll - and by
// TakeReconciliation when a count names one, once that location has been
// retired (account.inactive_on set, PRD section 6 and 7.8). Nothing is
// written. Retired means inactive_on is non-NULL, whatever date it holds and
// whatever the posting's own occurred_on is - the same reading
// PostPurposeMove and the SPA's pickers already apply. Reinstating the
// location (PATCH inactive_on null) is the way back.
//
// PostPurposeMove keeps its own ErrPurposeMoveAccountInactive and its own
// wire code: that API is already shipped and the SPA already speaks it.
//
// Deliberately not applied to the two postings that inherit their location
// from the row they correct - ReverseDuesPayment and PostPurposeCorrection:
// the treasurer chooses nothing there, and refusing would make a mistake
// on a since-retired location uncorrectable (CLAUDE.md rule 3).
var ErrAccountInactive = errors.New("ledger: the account is retired")

// ErrReconciliationMissingLocation is returned by TakeReconciliation when
// the snapshot leaves out an active location of the fund: Cek kas counts
// each active location, never a subset (PRD section 7.8). A subset would
// freeze a snapshot that reads "cocok" while a location nobody counted
// held the discrepancy. Nothing is written.
var ErrReconciliationMissingLocation = errors.New("ledger: the count omits an active location")
