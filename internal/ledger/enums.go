package ledger

// The closed sets the schema's CHECK constraints define. The types name each
// set in code; the CHECK stays the single source of truth for membership
// (ADR-027), so nothing here validates.

// Direction is transaction.direction: which way the money moved.
type Direction string

// The two directions.
const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

// TransactionKind is transaction.kind.
type TransactionKind string

// Every transaction kind the schema admits.
const (
	TransactionKindOpening       TransactionKind = "opening"
	TransactionKindNormal        TransactionKind = "normal"
	TransactionKindDues          TransactionKind = "dues"
	TransactionKindReimbursement TransactionKind = "reimbursement"
	TransactionKindAdjustment    TransactionKind = "adjustment"
	TransactionKindTransfer      TransactionKind = "transfer"
)

// AccountKind is account.kind: the kind of location the money sits in.
type AccountKind string

// The two kinds of location.
const (
	AccountKindCash AccountKind = "cash"
	AccountKindBank AccountKind = "bank"
)

// PurposeKind is purpose.kind.
type PurposeKind string

// Every purpose kind the schema admits.
const (
	PurposeKindMain        PurposeKind = "main"
	PurposeKindIncidental  PurposeKind = "incidental"
	PurposeKindPassThrough PurposeKind = "pass_through"
)

// TransferKind is transfer.kind.
type TransferKind string

// The two kinds of value-neutral movement.
const (
	TransferKindBetweenAccounts TransferKind = "between_accounts"
	TransferKindReclassPurpose  TransferKind = "reclass_purpose"
)

// Resolution is reconciliation_line.resolution.
type Resolution string

// Every way a counted line can be resolved.
const (
	ResolutionMatched    Resolution = "matched"
	ResolutionEntryAdded Resolution = "entry_added"
	ResolutionAdjusted   Resolution = "adjusted"
	ResolutionLeftOpen   Resolution = "left_open"
)
