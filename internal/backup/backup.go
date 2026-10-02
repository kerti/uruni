// Package backup is ADR-012's implementation: the whole fund, as one zip a
// session-gated route streams to the treasurer, and (M6.38, #325) the
// importer that restores it. Both halves live here together on the
// maintainer's own ruling in ADR-012 - "the logic lives in one package so
// that stays cheap" - rather than export and import drifting into two
// packages that quietly disagree about the shape between them.
//
// This slice (#323) is the export half only. Nothing here writes to the
// database; Import has no code yet.
package backup

// FormatVersion is uruni.json's own integer version (ADR-012's
// "compatibility across versions" paragraph) - separate from the app's own
// build version (ADR-018), and bumped only when the exported shape itself
// changes. Through 0.x the importer accepts only its own FormatVersion, so
// a bump is a conscious, load-bearing act, not a side effect of an
// unrelated release.
//
// TestGoldenFixtureMatchesExport (golden_test.go) is what makes a bump
// conscious: any change to a table this package exports, or to the shape
// below, fails that test until testdata/golden.json is regenerated and
// FormatVersion is bumped alongside it - the schema-change-breaks-a-test
// promise ADR-012 makes.
const FormatVersion int64 = 1

// Document is uruni.json's root object: every table but session (ADR-012 -
// sessions are never exported, so a restore always logs everyone out), ids
// preserved verbatim, plus the totals block a restore proves its numbers
// against. Every field name doubles as its own json tag, snake_case to
// match the schema's own column names - a restored file reads like the
// database it came from, not like a Go struct.
//
// Nothing instance-level appears here on purpose (ADR-012's "the file stays
// tenant-shaped"): no config, no instance id, no filesystem path. A
// receipt's Path is the one exception worth naming explicitly - it is the
// server-generated filename under URUNI_UPLOADS_DIR (ADR-011), not an
// absolute path, and the matching bytes sit beside it in the zip's
// receipts/ folder under that same name.
//
// Table order follows the migration file's own CREATE TABLE order
// (00001_schema.sql) - already the dependency order ADR-024 documents
// (fund -> account/purpose/dues_tier/dues_rate/member/transfer/
// reimbursement -> transaction -> receipt -> reconciliation/
// reconciliation_line -> incidental/incidental_recipient), plus "user"
// first since nothing depends on it. Row order within each table is
// whatever query this package already had reason to write for it - most
// reuse an existing ORDER BY id (or an existing fully-ordered listing);
// the few added for this slice (ListUsers, ListDuesRatesByFund,
// ListReceiptsByFund, ListReconciliationLinesByFund,
// ListIncidentalRecipientsByFund) order by id (or, where a table has no
// single-column id, by its primary key columns) purely so the export is
// byte-for-byte reproducible across two runs against the same database -
// not a claim that id order means anything (CLAUDE.md's "no primary-key
// order" rule), which is why that reasoning is spelled out at each query
// rather than assumed.
type Document struct {
	FormatVersion int64 `json:"format_version"`

	Users     []User     `json:"users"`
	Funds     []Fund     `json:"funds"`
	Accounts  []Account  `json:"accounts"`
	Purposes  []Purpose  `json:"purposes"`
	DuesTiers []DuesTier `json:"dues_tiers"`
	DuesRates []DuesRate `json:"dues_rates"`
	Members   []Member   `json:"members"`

	Transfers      []Transfer      `json:"transfers"`
	Reimbursements []Reimbursement `json:"reimbursements"`
	Transactions   []Transaction   `json:"transactions"`
	Receipts       []Receipt       `json:"receipts"`

	Reconciliations      []Reconciliation      `json:"reconciliations"`
	ReconciliationLines  []ReconciliationLine  `json:"reconciliation_lines"`
	Incidentals          []Incidental          `json:"incidentals"`
	IncidentalRecipients []IncidentalRecipient `json:"incidental_recipients"`

	Totals Totals `json:"totals"`
}

// User mirrors the "user" table verbatim, password hash included - the one
// reason the download card warns "termasuk info login" (ADR-012: "the zip
// carries a password hash and every name").
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash"`
	CreatedAt    int64  `json:"created_at"`
}

// Fund mirrors the "fund" table verbatim: the community's shared pool of
// money (CONTEXT.md).
type Fund struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Currency   string `json:"currency"`
	ReportSlug string `json:"report_slug"`
	CreatedAt  int64  `json:"created_at"`
}

// Account mirrors the "account" table verbatim: where money physically
// sits - cash or bank (CONTEXT.md).
type Account struct {
	ID         int64   `json:"id"`
	FundID     int64   `json:"fund_id"`
	Kind       string  `json:"kind"`
	Name       string  `json:"name"`
	CreatedAt  int64   `json:"created_at"`
	InactiveOn *string `json:"inactive_on"`
}

// Purpose mirrors the "purpose" table verbatim: what a transaction is for
// (CONTEXT.md).
type Purpose struct {
	ID        int64  `json:"id"`
	FundID    int64  `json:"fund_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// DuesTier mirrors the "dues_tier" table verbatim: a golongan the
// treasurer named (CONTEXT.md).
type DuesTier struct {
	ID        int64  `json:"id"`
	FundID    int64  `json:"fund_id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// DuesRate mirrors the "dues_rate" table verbatim: an effective-dated rate
// for one tier (CONTEXT.md).
type DuesRate struct {
	ID            int64  `json:"id"`
	TierID        int64  `json:"tier_id"`
	Amount        int64  `json:"amount"`
	EffectiveFrom string `json:"effective_from"`
	CreatedAt     int64  `json:"created_at"`
}

// Member mirrors the "member" table verbatim: a person in the group
// (CONTEXT.md).
type Member struct {
	ID         int64   `json:"id"`
	FundID     int64   `json:"fund_id"`
	Name       string  `json:"name"`
	TierID     *int64  `json:"tier_id"`
	JoinedOn   *string `json:"joined_on"`
	InactiveOn *string `json:"inactive_on"`
	CreatedAt  int64   `json:"created_at"`
}

// Transfer mirrors the "transfer" table verbatim: the pair-holder behind a
// value-neutral movement (CONTEXT.md).
type Transfer struct {
	ID                    int64  `json:"id"`
	FundID                int64  `json:"fund_id"`
	Kind                  string `json:"kind"`
	CorrectsTransactionID *int64 `json:"corrects_transaction_id"`
	// Reason is 'roll', 'allocation' or null (ADR-036). A file written
	// before the column has no such key, which decodes to nil - which is
	// what a pre-column roll is - so no format_version bump.
	Reason    *string `json:"reason"`
	CreatedAt int64   `json:"created_at"`
}

// Reimbursement mirrors the "reimbursement" table verbatim: a member's
// claim, off the ledger until settled (CONTEXT.md). The row's own
// "settled" state is derived, not stored - see convert.go's toReimbursements
// for why that computed column is left out here.
type Reimbursement struct {
	ID         int64   `json:"id"`
	FundID     int64   `json:"fund_id"`
	MemberID   int64   `json:"member_id"`
	PurposeID  int64   `json:"purpose_id"`
	Amount     int64   `json:"amount"`
	IncurredOn string  `json:"incurred_on"`
	WaivedOn   *string `json:"waived_on"`
	Note       *string `json:"note"`
	CreatedAt  int64   `json:"created_at"`
}

// Transaction mirrors the "transaction" table verbatim: one immutable
// posted ledger entry (CONTEXT.md, CLAUDE.md rule 3).
type Transaction struct {
	ID                    int64   `json:"id"`
	FundID                int64   `json:"fund_id"`
	AccountID             int64   `json:"account_id"`
	PurposeID             int64   `json:"purpose_id"`
	Direction             string  `json:"direction"`
	Amount                int64   `json:"amount"`
	OccurredOn            string  `json:"occurred_on"`
	Kind                  string  `json:"kind"`
	MemberID              *int64  `json:"member_id"`
	DuesPeriod            *string `json:"dues_period"`
	ReimbursementID       *int64  `json:"reimbursement_id"`
	TransferID            *int64  `json:"transfer_id"`
	ReversesTransactionID *int64  `json:"reverses_transaction_id"`
	Note                  *string `json:"note"`
	CreatedAt             int64   `json:"created_at"`
}

// Receipt mirrors the "receipt" table verbatim. Path is the stored,
// server-generated filename under URUNI_UPLOADS_DIR (ADR-011) - the same
// name the zip's receipts/ entry for this row uses.
type Receipt struct {
	ID              int64  `json:"id"`
	FundID          int64  `json:"fund_id"`
	TransactionID   *int64 `json:"transaction_id"`
	ReimbursementID *int64 `json:"reimbursement_id"`
	Path            string `json:"path"`
	UploadedAt      int64  `json:"uploaded_at"`
}

// Reconciliation mirrors the "reconciliation" table verbatim: a frozen
// count of the real money (CONTEXT.md).
type Reconciliation struct {
	ID                   int64   `json:"id"`
	FundID               int64   `json:"fund_id"`
	PerformedAt          int64   `json:"performed_at"`
	ThroughTransactionID *int64  `json:"through_transaction_id"`
	Note                 *string `json:"note"`
	CreatedAt            int64   `json:"created_at"`
}

// ReconciliationLine mirrors the "reconciliation_line" table verbatim: one
// counted location within a snapshot (CONTEXT.md).
type ReconciliationLine struct {
	ID                      int64  `json:"id"`
	FundID                  int64  `json:"fund_id"`
	ReconciliationID        int64  `json:"reconciliation_id"`
	AccountID               int64  `json:"account_id"`
	RecordedAmount          int64  `json:"recorded_amount"`
	ActualAmount            int64  `json:"actual_amount"`
	DifferenceAmount        int64  `json:"difference_amount"`
	Resolution              string `json:"resolution"`
	AdjustmentTransactionID *int64 `json:"adjustment_transaction_id"`
}

// Incidental mirrors the "incidental" table verbatim: an envelope's
// lifecycle, 1:1 with its purpose row (CONTEXT.md).
type Incidental struct {
	PurposeID        int64   `json:"purpose_id"`
	Occasion         string  `json:"occasion"`
	TargetAmount     *int64  `json:"target_amount"`
	OpenedOn         string  `json:"opened_on"`
	ClosedOn         *string `json:"closed_on"`
	MinimumPerMember *int64  `json:"minimum_per_member"`
	CreatedAt        int64   `json:"created_at"`
}

// IncidentalRecipient has no single-column id (its primary key is the
// (fund_id, purpose_id, member_id) triple) - every field is part of the
// key, so all three are exported verbatim.
type IncidentalRecipient struct {
	FundID    int64 `json:"fund_id"`
	PurposeID int64 `json:"purpose_id"`
	MemberID  int64 `json:"member_id"`
}

// Totals is ADR-012's "a restore proves its numbers": derived balance per
// fund, per account (location) and per purpose, computed by the same
// internal/ledger functions the home screen's GET /api/balances uses
// (FundBalance, AccountBalance, PurposeBalance) - never re-summed here or
// in SQL. A future importer recomputes the same three calls against the
// rows it just inserted and refuses to commit on any mismatch.
type Totals struct {
	Funds    []FundTotal    `json:"funds"`
	Accounts []AccountTotal `json:"accounts"`
	Purposes []PurposeTotal `json:"purposes"`
}

// FundTotal is one fund's derived balance within Totals.
type FundTotal struct {
	FundID  int64 `json:"fund_id"`
	Balance int64 `json:"balance"`
}

// AccountTotal is one account's derived balance within Totals.
type AccountTotal struct {
	AccountID int64 `json:"account_id"`
	Balance   int64 `json:"balance"`
}

// PurposeTotal is one purpose's derived balance within Totals.
type PurposeTotal struct {
	PurposeID int64 `json:"purpose_id"`
	Balance   int64 `json:"balance"`
}
