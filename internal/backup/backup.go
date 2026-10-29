// Package backup is ADR-012's implementation: the whole fund as one zip a
// session-gated route streams to the treasurer, the daily dumps the server
// writes, and the restore that reads one back. They live in one package on
// ADR-012's ruling ("the logic lives in one package so that stays cheap"), so
// export and import cannot drift into disagreeing about the shape between
// them.
//
// Export and the dumps only read the database; Restore (restore.go) is the
// one place here that writes it.
package backup

// FormatVersion is uruni.json's integer version (ADR-012's "compatibility
// across versions"), separate from the app's build version (ADR-018) and
// bumped only when the exported shape changes. The importer reads its own
// FormatVersion and upgrades every older one it still has a step for
// (upgrade.go); a version it has no step for is refused, so a bump is a
// conscious act.
//
// TestGoldenFixtureMatchesExport (golden_test.go) makes it conscious: any
// change to an exported table or the shape below fails that test until
// testdata/golden.json is regenerated (ADR-012's schema-change-breaks-a-test
// promise). Whether FormatVersion bumps with it is the 0.x rule's question: a
// bump is only allowed in the PR that ships the step converting the previous
// format, so the last release's backup stays restorable.
//
// 2 (ADR-040, ADR-041): a member's tier is effective-dated (the member_tiers
// array carries every member_tier row, superseded ones included; members[].tier_id
// is gone) and funds[].time_zone is one of Asia/Jakarta, Asia/Makassar,
// Asia/Jayapura. Version 1 files are upgraded in ParseUpload (the step fills
// Asia/Jakarta); any other zone, a missing field included, is refused there.
const FormatVersion int64 = 2

// Document is uruni.json's root object: every table but session and
// idempotency_key (ADR-012: neither is exported, so a restore logs everyone out
// and forgets every idempotency key), ids preserved
// verbatim, plus the totals block a restore proves its numbers against. Field
// names are snake_case json tags matching the schema's columns, so a restored
// file reads like the database it came from.
//
// Nothing instance-level appears here (ADR-012: "the file stays
// tenant-shaped"): no config, instance id or filesystem path. A receipt's
// Path is the one exception worth naming: the server-generated filename
// under URUNI_UPLOADS_DIR (ADR-011), not an absolute path, with the bytes
// beside it in the zip's receipts/ folder under that name.
//
// Table order follows the migration's CREATE TABLE order (00001_schema.sql),
// already the dependency order ADR-024 documents (fund ->
// account/purpose/dues_tier/dues_rate/member/member_tier/transfer/reimbursement ->
// transaction -> receipt -> reconciliation/reconciliation_line ->
// incidental/incidental_recipient), plus "user" first since nothing depends
// on it. Row order within a table is whatever query already existed for it
// (mostly ORDER BY id); the queries added for the export (ListUsers,
// ListDuesRatesByFund, ListMemberTiersForExport, ListReceiptsByFund,
// ListReconciliationLinesByFund, ListIncidentalRecipientsByFund) order by id, or by primary key columns
// where there is no single id, only so the export is byte-for-byte
// reproducible across two runs. That is not a claim that id order means
// anything (CLAUDE.md's "no primary-key order" rule); each query spells it out.
type Document struct {
	FormatVersion int64 `json:"format_version"`

	Users     []User     `json:"users"`
	Funds     []Fund     `json:"funds"`
	Accounts  []Account  `json:"accounts"`
	Purposes  []Purpose  `json:"purposes"`
	DuesTiers []DuesTier `json:"dues_tiers"`
	DuesRates []DuesRate `json:"dues_rates"`
	Members   []Member   `json:"members"`
	// MemberTiers holds every member_tier row, superseded ones too: the trail is
	// the data (ADR-040).
	MemberTiers []MemberTier `json:"member_tiers"`

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

// User mirrors the "user" table verbatim, password hash included: why the
// download card warns "termasuk info login" (ADR-012: the zip carries a
// password hash and every name).
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
	TimeZone   string `json:"time_zone"`
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

// DuesTier mirrors the "dues_tier" table verbatim: a tier the
// treasurer named (CONTEXT.md).
type DuesTier struct {
	ID        int64  `json:"id"`
	FundID    int64  `json:"fund_id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// DuesRate mirrors the "dues_rate" table verbatim: an effective-dated rate
// for one tier (CONTEXT.md). Every row is exported, superseded and deleting ones
// included (ADR-040). A v1 file carries neither Correction nor Deleted; the v1 to
// v2 step reads them as 0 and false.
type DuesRate struct {
	ID            int64  `json:"id"`
	TierID        int64  `json:"tier_id"`
	Amount        int64  `json:"amount"`
	EffectiveFrom string `json:"effective_from"`
	Correction    int64  `json:"correction"`
	Deleted       bool   `json:"deleted"`
	CreatedAt     int64  `json:"created_at"`
}

// Member mirrors the "member" table verbatim: a person in the group
// (CONTEXT.md). It carries no tier (ADR-040): that is MemberTier rows.
type Member struct {
	ID         int64   `json:"id"`
	FundID     int64   `json:"fund_id"`
	Name       string  `json:"name"`
	JoinedOn   string  `json:"joined_on"`
	InactiveOn *string `json:"inactive_on"`
	CreatedAt  int64   `json:"created_at"`
}

// MemberTier mirrors the "member_tier" table verbatim: a member's tier from a
// month on, or no tier (null) from that month. Correction numbers the restatements
// of one month; the highest stands and the rest are superseded but kept.
type MemberTier struct {
	ID            int64  `json:"id"`
	FundID        int64  `json:"fund_id"`
	MemberID      int64  `json:"member_id"`
	TierID        *int64 `json:"tier_id"`
	EffectiveFrom string `json:"effective_from"`
	Correction    int64  `json:"correction"`
	CreatedAt     int64  `json:"created_at"`
}

// Transfer mirrors the "transfer" table verbatim: the pair-holder behind a
// value-neutral movement (CONTEXT.md).
type Transfer struct {
	ID                    int64  `json:"id"`
	FundID                int64  `json:"fund_id"`
	Kind                  string `json:"kind"`
	CorrectsTransactionID *int64 `json:"corrects_transaction_id"`
	// Reason is 'roll', 'allocation' or null (ADR-036). A file written before the
	// column has no such key, which decodes to nil, what a pre-column roll is, so
	// no format_version bump.
	Reason    *string `json:"reason"`
	CreatedAt int64   `json:"created_at"`
}

// Reimbursement mirrors the "reimbursement" table verbatim: a member's claim,
// off the ledger until settled (CONTEXT.md). Its "settled" state is derived,
// not stored; see convert.go's toReimbursements.
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
// server-generated filename under URUNI_UPLOADS_DIR (ADR-011), the name the
// zip's receipts/ entry uses.
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
// (fund_id, purpose_id, member_id) triple); every field is part of the key,
// so all three are exported verbatim.
type IncidentalRecipient struct {
	FundID    int64 `json:"fund_id"`
	PurposeID int64 `json:"purpose_id"`
	MemberID  int64 `json:"member_id"`
}

// Totals is ADR-012's "a restore proves its numbers": derived balance per
// fund, account (location) and purpose, read with the generated queries the
// home screen's GET /api/balances composes (FundBalance, AccountBalance,
// PurposeBalance), in the one read transaction that read the rows beside them
// (#496). Every path that makes or accepts a file recomputes them from its
// rows and compares exactly (totals.go): export before writing, inspect
// before the password prompt, and Restore against the rows it inserted,
// refusing to commit on any mismatch.
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
