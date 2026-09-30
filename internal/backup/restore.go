// restore.go is #325's own half of ADR-012: parsing and validating an
// uploaded backup zip (ParseUpload), previewing what it would change
// against the live database (BuildPreview), and the atomic restore itself
// (Restore). All three are pure package functions rather than methods on a
// stateful type - restore is a one-shot operation with nothing to carry
// between calls, and a future CLI `uruni import` (ADR-012's own deferred
// promise) calls exactly this trio: read the file, ParseUpload it, and
// either print BuildPreview's own numbers or skip straight to Restore.
//
// (A blank line separates this from `package backup` on purpose - dumps.go's
// own comment explains why: without it, revive's package-comments check
// reads this as *the* package doc comment, which export.go already owns.)

package backup

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // registers the JPEG decoder - every receipt this package
	// ever wrote is one (receipt_image.go always re-encodes to JPEG), and a
	// restore validates whatever the zip actually carries, not only that.
	_ "image/png" // registers the PNG decoder - decode-only, same reasoning
	// internal/http/receipt_image.go gives for the same blank import.
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp" // registers the WebP decoder - decode-only,
	// same reasoning as receipt_image.go's own blank import.

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// Upload caps (issue #325's own acceptance criteria): 256 MB of *compressed*
// body - enforced by the HTTP handler wrapping the request in
// http.MaxBytesReader before a single byte reaches this package, so
// MaxUploadBytes exists here only as the one named constant both layers
// read, not as a second enforcement point - 1 GB of *unzipped* content,
// counted from bytes actually copied out of the zip as ParseUpload reads
// them, never from a zip entry's own declared UncompressedSize64 (a
// dishonest header is exactly what a zip bomb lies about), and 5,000
// entries. All three together are what makes an upload zip-bomb safe:
// nothing this package ever does can be made to allocate more than
// MaxUnzippedBytes of decompressed data, however small the compressed file
// claims to be.
//
// var, not const: restore_test.go temporarily shrinks these to exercise
// the two size caps without actually building gigabyte-scale test fixtures
// - the values below are what the running binary always uses outside a
// test that says otherwise.
var (
	MaxUploadBytes   int64 = 256 << 20
	MaxUnzippedBytes int64 = 1 << 30
	MaxZipEntries          = 5000
)

// maxReceiptPixelsForRestore mirrors internal/http/receipt_image.go's own
// maxReceiptPixels (ADR-011's decode-check cap) - duplicated, not shared,
// because internal/http already imports internal/backup (the download and
// list routes) and the reverse import would cycle. If receipt_image.go's
// own cap ever moves, this one has to move with it; there is no test that
// catches that drift today; the human-authored comment carries the load a
// test can't.
const maxReceiptPixelsForRestore = 50_000_000

// Sentinel errors ParseUpload, BuildPreview and Restore return, wrapped
// with the specific detail (a byte count, a table name, an id) that made
// each one fire - callers distinguish the case with errors.Is against one
// of these, the same shape ledger's own ErrInvalidArgument family already
// uses.
var (
	// ErrFormatVersionNewer and ErrFormatVersionOlder are deliberately
	// distinct (issue #325's own acceptance criteria): a newer file means
	// the binary needs upgrading first; an older one is refused outright
	// through 0.x (ADR-012's "restoring onto an older binary is refused -
	// rolling the image back is the operator's job" - there is no
	// per-version upgrade chain yet, so "older" and "newer" both refuse
	// today, but for different reasons a treasurer would act on
	// differently).
	ErrFormatVersionNewer = errors.New("backup: the file was made by a newer version of Uruni than this server runs")
	ErrFormatVersionOlder = errors.New("backup: the file was made by an older version of Uruni than this server understands")
	// ErrMalformedBackup covers every shape problem short of the version
	// mismatch above: not a zip, no uruni.json inside it, an unknown field
	// DisallowUnknownFields caught, or a receipt path that is not a bare
	// filename (see ParseUpload's own check - a decoded backup is untrusted
	// input, unlike a live receipt row's path, which is always
	// server-generated).
	ErrMalformedBackup  = errors.New("backup: the file is not a valid Uruni backup")
	ErrTooManyEntries   = errors.New("backup: the zip holds more files than a backup ever should")
	ErrUnzippedTooLarge = errors.New("backup: the zip's contents are larger, unzipped, than a backup ever should be")
	// ErrBadReceiptImage is issue #325's "bad image" refusal case: a
	// receipt file present in the zip that fails the same decode checks
	// ADR-011 already applies on upload. A *missing* file is not this -
	// ParseUpload skips it silently (ruling 5: "a missing image never
	// blocks a restore").
	ErrBadReceiptImage = errors.New("backup: a receipt image in the file could not be read")
	// ErrTotalsMismatch is Restore's own refusal, raised inside the write
	// transaction after every row is inserted and before COMMIT - the
	// transaction rolls back with nothing changed, exactly like every
	// other error Restore returns.
	ErrTotalsMismatch = errors.New("backup: the restored totals did not match the file's own totals")
)

// ParsedUpload is ParseUpload's result: the decoded, validated document and
// the receipt image bytes the zip actually carried, keyed by the bare
// filename their own Receipt.Path names. BuildPreview and Restore both take
// this, never a raw zip again - the CLI import ADR-012 defers calls
// ParseUpload once and hands the same value to whichever of the two it
// needs.
type ParsedUpload struct {
	Document Document
	Receipts map[string][]byte
}

// ParseUpload decodes and validates an uploaded backup zip - entirely in
// memory, entirely read-only (no database call, no filesystem write) - so
// it can run as the HTTP handler's own "inspect" step, well before the
// treasurer has typed her password to confirm anything.
//
// Order matters: the two caps (entries, unzipped bytes) are enforced while
// reading, before either is trusted; format_version is checked next,
// against the raw bytes, so a newer or older file gets its own distinct
// error before DisallowUnknownFields ever runs against a shape this
// server's Document might not even recognise; the strict decode runs only
// once format_version already matches; and every receipt image the zip
// actually carries is decode-checked last; ADR-012's "a restore proves its
// numbers" (the totals check) is not this function's job - Restore verifies
// those after inserting, since it needs a live transaction to recompute
// them against.
func ParseUpload(zipBytes []byte) (ParsedUpload, error) {
	if int64(len(zipBytes)) > MaxUploadBytes {
		return ParsedUpload{}, fmt.Errorf("%w: %d bytes, more than the %d byte cap", ErrTooManyEntries, len(zipBytes), MaxUploadBytes)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return ParsedUpload{}, fmt.Errorf("%w: not a zip file: %v", ErrMalformedBackup, err) //nolint:errorlint // wrapping two errors in one Errorf is intentional here; ErrMalformedBackup is the one callers match against
	}
	if len(zr.File) > MaxZipEntries {
		return ParsedUpload{}, fmt.Errorf("%w: %d entries, more than the %d a backup ever holds", ErrTooManyEntries, len(zr.File), MaxZipEntries)
	}

	var jsonBytes []byte
	receipts := map[string][]byte{}
	remaining := MaxUnzippedBytes

	for _, f := range zr.File {
		data, n, err := readCappedEntry(f, remaining)
		if err != nil {
			return ParsedUpload{}, err
		}
		remaining -= n

		switch {
		case f.Name == jsonFilename:
			jsonBytes = data
		case strings.HasPrefix(f.Name, receiptsDir):
			receipts[strings.TrimPrefix(f.Name, receiptsDir)] = data
		default:
			// An entry this package never wrote. Not itself a reason to
			// refuse the whole file - the two caps above already bound
			// what reading it can cost - so it is silently ignored, the
			// same tolerance ListDumps extends to a foreign file sitting
			// in the backup directory.
		}
	}

	if jsonBytes == nil {
		return ParsedUpload{}, fmt.Errorf("%w: no %s entry", ErrMalformedBackup, jsonFilename)
	}

	if err := checkFormatVersion(jsonBytes); err != nil {
		return ParsedUpload{}, err
	}

	var doc Document
	dec := json.NewDecoder(bytes.NewReader(jsonBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return ParsedUpload{}, fmt.Errorf("%w: decoding %s: %v", ErrMalformedBackup, jsonFilename, err) //nolint:errorlint
	}

	for _, r := range doc.Receipts {
		// r.Path came out of an uploaded, untrusted file - unlike a live
		// receipt row's Path, which internal/http/receipts.go only ever
		// sets from randomReceiptFilename. A bare-filename check here is
		// what stops a crafted backup naming "../../etc/passwd" and
		// reaching writeReceiptFileNamed's os.Rename below with it.
		if r.Path != filepath.Base(r.Path) {
			return ParsedUpload{}, fmt.Errorf("%w: receipt path %q is not a bare filename", ErrMalformedBackup, r.Path)
		}
		data, ok := receipts[r.Path]
		if !ok {
			continue // ADR-012 ruling 5: a missing image never blocks a restore
		}
		if err := validateReceiptImageBytes(data); err != nil {
			return ParsedUpload{}, err
		}
	}

	return ParsedUpload{Document: doc, Receipts: receipts}, nil
}

// readCappedEntry opens and fully reads one zip entry, refusing to read
// past remaining+1 bytes regardless of what the entry's own header claims
// to hold - the read itself, not the header, is what a zip bomb has to lie
// through, and this is what makes that lie visible.
func readCappedEntry(f *zip.File, remaining int64) ([]byte, int64, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, 0, fmt.Errorf("%w: opening %s: %v", ErrMalformedBackup, f.Name, err) //nolint:errorlint
	}
	defer func() { _ = rc.Close() }()

	data, err := io.ReadAll(io.LimitReader(rc, remaining+1))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: reading %s: %v", ErrMalformedBackup, f.Name, err) //nolint:errorlint
	}
	if int64(len(data)) > remaining {
		return nil, 0, fmt.Errorf("%w: more than %d bytes unzipped", ErrUnzippedTooLarge, MaxUnzippedBytes)
	}
	return data, int64(len(data)), nil
}

// checkFormatVersion peeks format_version out of the raw JSON bytes before
// any strict, full decode is attempted - so a file from a future,
// differently-shaped version of this schema still gets the specific
// "newer version" refusal instead of tripping DisallowUnknownFields with a
// generic shape error.
func checkFormatVersion(jsonBytes []byte) error {
	var peek struct {
		FormatVersion int64 `json:"format_version"`
	}
	if err := json.Unmarshal(jsonBytes, &peek); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedBackup, err) //nolint:errorlint
	}
	switch {
	case peek.FormatVersion > FormatVersion:
		return fmt.Errorf("%w: file is format_version %d, this server understands %d", ErrFormatVersionNewer, peek.FormatVersion, FormatVersion)
	case peek.FormatVersion < FormatVersion:
		return fmt.Errorf("%w: file is format_version %d, this server understands %d", ErrFormatVersionOlder, peek.FormatVersion, FormatVersion)
	default:
		return nil
	}
}

// validateReceiptImageBytes is ADR-011's decode check, restore's own copy:
// the image must be decodable and within the same pixel ceiling
// receipt_image.go enforces on ordinary upload. It does not downscale or
// re-encode - Restore writes these bytes to the uploads volume verbatim,
// since they are already whatever the live instance had written there the
// day the backup was taken.
func validateReceiptImageBytes(data []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadReceiptImage, err) //nolint:errorlint
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxReceiptPixelsForRestore {
		return fmt.Errorf("%w: %dx%d exceeds the pixel cap", ErrBadReceiptImage, cfg.Width, cfg.Height)
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("%w: %v", ErrBadReceiptImage, err) //nolint:errorlint
	}
	return nil
}

// FundStatus is one fund's own place in a restore preview: whether it
// exists on both sides, only live, or only in the file (ADR-012's
// per-fund confirm-step copy, added by this issue's own ADR amendment).
type FundStatus string

// The three shapes a whole-instance replace can produce for one fund - see
// FundStatus's own comment.
const (
	FundKept    FundStatus = "kept"
	FundRemoved FundStatus = "removed"
	FundAdded   FundStatus = "added"
)

// FundPreview is one line of the confirm step's per-fund list.
// TransactionsLost and CutoffDate are meaningful only when Status is
// FundKept - ADR-012's restore-scope paragraph: "live transactions whose id
// is past the file's highest id... e.g. '12 transaksi setelah 26 Sep akan
// hilang'". CutoffDate is empty when the file carries no transaction for
// this fund at all yet, in which case every live transaction counts as
// lost.
type FundPreview struct {
	FundID           int64      `json:"fund_id"`
	Name             string     `json:"name"`
	Status           FundStatus `json:"status"`
	TransactionsLost int64      `json:"transactions_lost"`
	CutoffDate       string     `json:"cutoff_date,omitempty"`
}

// Preview is BuildPreview's whole result: what the confirm step shows
// before the treasurer types her password (issue #325's acceptance
// criteria: "date, funds, total, per-fund line").
type Preview struct {
	// Date stands in for "when was this backup taken" - the newest
	// created_at (or, for a receipt, uploaded_at) found anywhere in the
	// file. Document itself carries no wall-clock "exported at" field
	// (backup.go's own comment on why), and a filename is not a safe
	// substitute: a direct GET /api/backup download's name carries a date,
	// but a treasurer can rename the file before re-uploading it, and an
	// upload's own name is never trusted for anything this package does.
	Date  string        `json:"date"`
	Total int64         `json:"total"`
	Funds []FundPreview `json:"funds"`
}

// BuildPreview compares doc against the live database and reports what a
// restore would change - read-only, no transaction, safe to call as often
// as the treasurer re-opens the confirm dialog.
func BuildPreview(ctx context.Context, q store.Querier, doc Document) (Preview, error) {
	liveFunds, err := q.ListFunds(ctx)
	if err != nil {
		return Preview{}, fmt.Errorf("backup: listing live funds: %w", err)
	}

	fileFundByID := make(map[int64]Fund, len(doc.Funds))
	for _, f := range doc.Funds {
		fileFundByID[f.ID] = f
	}

	type cutoff struct {
		maxID int64
		date  string
	}
	cutoffs := make(map[int64]cutoff, len(doc.Funds))
	for _, t := range doc.Transactions {
		c := cutoffs[t.FundID]
		if t.ID > c.maxID {
			c.maxID, c.date = t.ID, t.OccurredOn
		}
		cutoffs[t.FundID] = c
	}

	var total money.Amount
	for _, ft := range doc.Totals.Funds {
		total, err = total.Add(money.FromDB(ft.Balance))
		if err != nil {
			return Preview{}, fmt.Errorf("backup: summing the file's own fund totals: %w", err)
		}
	}

	preview := Preview{Date: newestMoment(doc), Total: total.Int64(), Funds: []FundPreview{}}

	seen := make(map[int64]bool, len(liveFunds))
	for _, live := range liveFunds {
		seen[live.ID] = true
		fileFund, inFile := fileFundByID[live.ID]
		if !inFile {
			preview.Funds = append(preview.Funds, FundPreview{FundID: live.ID, Name: live.Name, Status: FundRemoved})
			continue
		}

		c := cutoffs[live.ID]
		liveTxns, err := q.ListTransactionsByFund(ctx, live.ID)
		if err != nil {
			return Preview{}, fmt.Errorf("backup: listing live transactions for fund %d: %w", live.ID, err)
		}
		var lost int64
		for _, t := range liveTxns {
			if t.ID > c.maxID {
				lost++
			}
		}
		preview.Funds = append(preview.Funds, FundPreview{
			FundID: live.ID, Name: fileFund.Name, Status: FundKept,
			TransactionsLost: lost, CutoffDate: c.date,
		})
	}
	for _, f := range doc.Funds {
		if !seen[f.ID] {
			preview.Funds = append(preview.Funds, FundPreview{FundID: f.ID, Name: f.Name, Status: FundAdded})
		}
	}

	return preview, nil
}

// newestMoment scans every timestamp Document carries (created_at, or a
// receipt's own uploaded_at) and returns the latest one as a Jakarta
// calendar date - see Preview.Date's own comment for why this, rather than
// a stored field or the upload's filename, is what "date" means here.
func newestMoment(doc Document) string {
	var latest int64
	consider := func(t int64) {
		if t > latest {
			latest = t
		}
	}
	for _, x := range doc.Funds {
		consider(x.CreatedAt)
	}
	for _, x := range doc.Accounts {
		consider(x.CreatedAt)
	}
	for _, x := range doc.Purposes {
		consider(x.CreatedAt)
	}
	for _, x := range doc.DuesTiers {
		consider(x.CreatedAt)
	}
	for _, x := range doc.DuesRates {
		consider(x.CreatedAt)
	}
	for _, x := range doc.Members {
		consider(x.CreatedAt)
	}
	for _, x := range doc.Transfers {
		consider(x.CreatedAt)
	}
	for _, x := range doc.Reimbursements {
		consider(x.CreatedAt)
	}
	for _, x := range doc.Transactions {
		consider(x.CreatedAt)
	}
	for _, x := range doc.Receipts {
		consider(x.UploadedAt)
	}
	for _, x := range doc.Reconciliations {
		consider(x.CreatedAt)
	}
	for _, x := range doc.Incidentals {
		consider(x.CreatedAt)
	}
	if latest == 0 {
		return ""
	}
	return time.Unix(latest, 0).In(jakarta).Format("2006-01-02")
}

// immutableTriggers names 00001_schema.sql's eight BEFORE UPDATE/DELETE
// triggers guarding "transaction", transfer, reconciliation and
// reconciliation_line. Restore drops every one of them for exactly the
// width of its own write transaction - SQLite has no "temporarily disable
// this trigger" short of dropping and recreating it - clears and
// re-populates the four tables they guard while they are gone, then
// recreates each one before COMMIT. The window is invisible to anything
// else: ADR-004's single connection (SetMaxOpenConns(1)) means no other
// statement can even be in flight while this transaction holds it.
//
// Only the names live here. Each trigger's body is read back out of
// sqlite_master inside the same transaction just before it is dropped, so
// a restore always reinstates whatever the schema currently says - an edit
// to a trigger in the migration file can never be silently reverted by a
// stale copy in this package. TestRestoreRecreatesImmutableTriggers proves
// each one still refuses after a restore.
var immutableTriggers = []string{
	"transaction_immutable_update",
	"transaction_immutable_delete",
	"transfer_immutable_update",
	"transfer_immutable_delete",
	"reconciliation_immutable_update",
	"reconciliation_immutable_delete",
	"reconciliation_line_immutable_update",
	"reconciliation_line_immutable_delete",
}

// Restore is the whole write side of ADR-012's restore path: a safety-net
// dump, additive receipt extraction, then one write transaction that
// clears every fund-scoped table, re-inserts exactly what parsed.Document
// holds with every id preserved, proves the totals, and commits - or
// changes nothing at all.
//
// sqlDB is the raw connection pool rather than a *ledger.Ledger or
// store.Querier alone: this is the one operation in the whole codebase
// that has to open its own transaction spanning virtually every table, and
// ledger.Ledger's own balance methods are bound to sqlDB directly (not to
// whatever transaction is currently open on it) - calling them from inside
// this function's own transaction would ask ADR-004's single connection
// for a second connection while the first is still checked out, which
// deadlocks rather than reading a stale value. verifyTotals below calls
// store.Queries.FundBalance/AccountBalance/PurposeBalance directly against
// this transaction's own store.New(tx) instead - the exact same generated
// queries l.FundBalance etc. call, just bound to the right connection.
//
// q and l are still needed for WritePreRestoreDump, which runs before this
// function's own transaction opens and is meant to read the database's
// current, live, still-uncommitted-to state - exactly what the top-level
// query and ledger objects already point at.
func Restore(ctx context.Context, sqlDB *sql.DB, q store.Querier, l *ledger.Ledger, uploadsDir, backupDir string, parsed ParsedUpload, now time.Time) error {
	if _, err := WritePreRestoreDump(ctx, q, l, uploadsDir, backupDir, now); err != nil {
		return fmt.Errorf("backup: writing the safety-net dump: %w", err)
	}

	for _, r := range parsed.Document.Receipts {
		data, ok := parsed.Receipts[r.Path]
		if !ok {
			continue // ruling 5: a missing image never blocks a restore
		}
		if err := writeReceiptFileNamed(uploadsDir, r.Path, data); err != nil {
			return fmt.Errorf("backup: extracting receipt %s: %w", r.Path, err)
		}
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("backup: beginning the restore transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has run

	if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
		return fmt.Errorf("backup: setting defer_foreign_keys: %w", err)
	}

	triggerSQL := make([]string, len(immutableTriggers))
	for i, name := range immutableTriggers {
		if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?", name).Scan(&triggerSQL[i]); err != nil {
			return fmt.Errorf("backup: reading trigger %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+name); err != nil {
			return fmt.Errorf("backup: dropping trigger %s: %w", name, err)
		}
	}

	tq := store.New(tx)

	if err := clearFundScopedTables(ctx, tq); err != nil {
		return err
	}
	if err := insertDocument(ctx, tq, parsed.Document); err != nil {
		return err
	}

	for i, name := range immutableTriggers {
		if _, err := tx.ExecContext(ctx, triggerSQL[i]); err != nil {
			return fmt.Errorf("backup: recreating trigger %s: %w", name, err)
		}
	}

	// Every session is gone after a restore (ADR-012: "sessions are never
	// exported... a restore logs everyone out"), inside the same
	// transaction as everything else - a rolled-back restore must leave
	// the treasurer's own session untouched too.
	if err := tq.DeleteAllSessions(ctx); err != nil {
		return fmt.Errorf("backup: clearing sessions: %w", err)
	}

	if err := verifyTotals(ctx, tq, parsed.Document); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("backup: committing the restore: %w", err)
	}
	return nil
}

// clearFundScopedTables deletes every row from every table this package
// restores, save "user" (never touched - ADR-012's login ruling) and
// "session" (cleared separately, inside Restore itself, matching
// DeleteAllSessions' own existing shape). Listed child-before-parent for a
// human reading it, though the order does not matter to SQLite itself:
// defer_foreign_keys=ON (set before this runs) defers every FK check in
// the whole transaction to COMMIT.
func clearFundScopedTables(ctx context.Context, q store.Querier) error {
	deletes := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"incidental_recipient", q.DeleteAllIncidentalRecipients},
		{"incidental", q.DeleteAllIncidentals},
		{"reconciliation_line", q.DeleteAllReconciliationLines},
		{"reconciliation", q.DeleteAllReconciliations},
		{"receipt", q.DeleteAllReceipts},
		{"transaction", q.DeleteAllTransactions},
		{"reimbursement", q.DeleteAllReimbursements},
		{"transfer", q.DeleteAllTransfers},
		{"member", q.DeleteAllMembers},
		{"dues_rate", q.DeleteAllDuesRates},
		{"dues_tier", q.DeleteAllDuesTiers},
		{"purpose", q.DeleteAllPurposes},
		{"account", q.DeleteAllAccounts},
		{"fund", q.DeleteAllFunds},
	}
	for _, d := range deletes {
		if err := d.fn(ctx); err != nil {
			return fmt.Errorf("backup: clearing %s: %w", d.name, err)
		}
	}
	return nil
}

// insertDocument writes every row doc holds, in Document's own field order
// - the migration file's dependency order (backup.go's own comment) - with
// one exception: doc.Transactions goes through restoreTransactionsInOrder
// instead of a plain loop, because the live transaction_named_row_shape
// trigger (ADR-012 obligation 1) still checks, at INSERT time, that a
// reversal's own target row already exists - a check no PRAGMA defers.
// Every other table's foreign keys are ordinary declarative ones, and
// defer_foreign_keys=ON already lets them land in any order.
func insertDocument(ctx context.Context, q store.Querier, doc Document) error {
	for _, f := range doc.Funds {
		if err := q.RestoreFund(ctx, store.RestoreFundParams{
			ID: f.ID, Name: f.Name, Currency: f.Currency, ReportSlug: f.ReportSlug, CreatedAt: f.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring fund %d: %w", f.ID, err)
		}
	}
	for _, a := range doc.Accounts {
		if err := q.RestoreAccount(ctx, store.RestoreAccountParams{
			ID: a.ID, FundID: a.FundID, Kind: a.Kind, Name: a.Name, CreatedAt: a.CreatedAt, InactiveOn: a.InactiveOn,
		}); err != nil {
			return fmt.Errorf("backup: restoring account %d: %w", a.ID, err)
		}
	}
	for _, p := range doc.Purposes {
		if err := q.RestorePurpose(ctx, store.RestorePurposeParams{
			ID: p.ID, FundID: p.FundID, Kind: p.Kind, Name: p.Name, CreatedAt: p.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring purpose %d: %w", p.ID, err)
		}
	}
	for _, dt := range doc.DuesTiers {
		if err := q.RestoreDuesTier(ctx, store.RestoreDuesTierParams{
			ID: dt.ID, FundID: dt.FundID, Name: dt.Name, CreatedAt: dt.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring dues tier %d: %w", dt.ID, err)
		}
	}
	for _, dr := range doc.DuesRates {
		if err := q.RestoreDuesRate(ctx, store.RestoreDuesRateParams{
			ID: dr.ID, TierID: dr.TierID, Amount: dr.Amount, EffectiveFrom: dr.EffectiveFrom, CreatedAt: dr.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring dues rate %d: %w", dr.ID, err)
		}
	}
	for _, m := range doc.Members {
		if err := q.RestoreMember(ctx, store.RestoreMemberParams{
			ID: m.ID, FundID: m.FundID, Name: m.Name, TierID: m.TierID,
			JoinedOn: m.JoinedOn, InactiveOn: m.InactiveOn, CreatedAt: m.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring member %d: %w", m.ID, err)
		}
	}
	for _, tr := range doc.Transfers {
		if err := q.RestoreTransfer(ctx, store.RestoreTransferParams{
			ID: tr.ID, FundID: tr.FundID, Kind: tr.Kind, CorrectsTransactionID: tr.CorrectsTransactionID, CreatedAt: tr.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring transfer %d: %w", tr.ID, err)
		}
	}
	for _, rb := range doc.Reimbursements {
		if err := q.RestoreReimbursement(ctx, store.RestoreReimbursementParams{
			ID: rb.ID, FundID: rb.FundID, MemberID: rb.MemberID, PurposeID: rb.PurposeID, Amount: rb.Amount,
			IncurredOn: rb.IncurredOn, WaivedOn: rb.WaivedOn, Note: rb.Note, CreatedAt: rb.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring reimbursement %d: %w", rb.ID, err)
		}
	}
	if err := restoreTransactionsInOrder(ctx, q, doc.Transactions); err != nil {
		return err
	}
	for _, rc := range doc.Receipts {
		if err := q.RestoreReceipt(ctx, store.RestoreReceiptParams{
			ID: rc.ID, FundID: rc.FundID, TransactionID: rc.TransactionID, ReimbursementID: rc.ReimbursementID,
			Path: rc.Path, UploadedAt: rc.UploadedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring receipt %d: %w", rc.ID, err)
		}
	}
	for _, rn := range doc.Reconciliations {
		if err := q.RestoreReconciliation(ctx, store.RestoreReconciliationParams{
			ID: rn.ID, FundID: rn.FundID, PerformedAt: rn.PerformedAt,
			ThroughTransactionID: rn.ThroughTransactionID, Note: rn.Note, CreatedAt: rn.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring reconciliation %d: %w", rn.ID, err)
		}
	}
	for _, rl := range doc.ReconciliationLines {
		if err := q.RestoreReconciliationLine(ctx, store.RestoreReconciliationLineParams{
			ID: rl.ID, FundID: rl.FundID, ReconciliationID: rl.ReconciliationID, AccountID: rl.AccountID,
			RecordedAmount: rl.RecordedAmount, ActualAmount: rl.ActualAmount, DifferenceAmount: rl.DifferenceAmount,
			Resolution: rl.Resolution, AdjustmentTransactionID: rl.AdjustmentTransactionID,
		}); err != nil {
			return fmt.Errorf("backup: restoring reconciliation line %d: %w", rl.ID, err)
		}
	}
	for _, in := range doc.Incidentals {
		if err := q.RestoreIncidental(ctx, store.RestoreIncidentalParams{
			PurposeID: in.PurposeID, Occasion: in.Occasion, TargetAmount: in.TargetAmount,
			OpenedOn: in.OpenedOn, ClosedOn: in.ClosedOn, MinimumPerMember: in.MinimumPerMember, CreatedAt: in.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring incidental %d: %w", in.PurposeID, err)
		}
	}
	for _, ir := range doc.IncidentalRecipients {
		if err := q.RestoreIncidentalRecipient(ctx, store.RestoreIncidentalRecipientParams{
			FundID: ir.FundID, PurposeID: ir.PurposeID, MemberID: ir.MemberID,
		}); err != nil {
			return fmt.Errorf("backup: restoring incidental recipient (fund %d, purpose %d, member %d): %w", ir.FundID, ir.PurposeID, ir.MemberID, err)
		}
	}
	return nil
}

// restoreTransactionsInOrder inserts every row in txns, a row at a time,
// but only once whatever row its own reverses_transaction_id names (if
// any) is already inserted - the one true dependency the live
// transaction_named_row_shape trigger enforces immediately, not deferred
// (ADR-012 obligation 1: "by dependency, not by id, since id order is not
// something new code may rely on"). A recursive insert-then-mark-visited
// walk, not a sort by id: the file's own array order is whatever
// ListTransactionsByFund happened to return it in (id ascending, in
// practice, but CLAUDE.md's own "no primary-key order" rule is exactly why
// this does not lean on that holding).
func restoreTransactionsInOrder(ctx context.Context, q store.Querier, txns []Transaction) error {
	byID := make(map[int64]Transaction, len(txns))
	for _, t := range txns {
		byID[t.ID] = t
	}
	inserted := make(map[int64]bool, len(txns))
	visiting := make(map[int64]bool, len(txns))

	var insert func(id int64) error
	insert = func(id int64) error {
		if inserted[id] {
			return nil
		}
		t, ok := byID[id]
		if !ok {
			// Names a row this file does not itself carry. Nothing to
			// insert on its behalf - if some other row's
			// reverses_transaction_id depends on it, the live trigger
			// refuses that insert on its own, with its own message.
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("%w: transaction %d's reverses_transaction_id cycles back to itself", ErrMalformedBackup, id)
		}
		visiting[id] = true
		if t.ReversesTransactionID != nil {
			if err := insert(*t.ReversesTransactionID); err != nil {
				return err
			}
		}
		if err := q.RestoreTransaction(ctx, store.RestoreTransactionParams{
			ID: t.ID, FundID: t.FundID, AccountID: t.AccountID, PurposeID: t.PurposeID,
			Direction: t.Direction, Amount: t.Amount, OccurredOn: t.OccurredOn, Kind: t.Kind,
			MemberID: t.MemberID, DuesPeriod: t.DuesPeriod, ReimbursementID: t.ReimbursementID,
			TransferID: t.TransferID, ReversesTransactionID: t.ReversesTransactionID,
			Note: t.Note, CreatedAt: t.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring transaction %d: %w", id, err)
		}
		inserted[id] = true
		visiting[id] = false
		return nil
	}

	for _, t := range txns {
		if err := insert(t.ID); err != nil {
			return err
		}
	}
	return nil
}

// verifyTotals is ADR-012's "a restore proves its numbers": every figure in
// doc.Totals, recomputed from the rows Restore just inserted, inside the
// same still-open transaction, and compared exactly (int64, no tolerance) -
// CLAUDE.md's own money rule. It calls store.Queries' generated
// FundBalance/AccountBalance/PurposeBalance directly rather than going
// through a *ledger.Ledger - see Restore's own doc comment for why a
// Ledger bound to sqlDB cannot be called from inside this transaction
// without deadlocking ADR-004's single connection.
func verifyTotals(ctx context.Context, q store.Querier, doc Document) error {
	accountFund := make(map[int64]int64, len(doc.Accounts))
	for _, a := range doc.Accounts {
		accountFund[a.ID] = a.FundID
	}
	purposeFund := make(map[int64]int64, len(doc.Purposes))
	for _, p := range doc.Purposes {
		purposeFund[p.ID] = p.FundID
	}

	for _, ft := range doc.Totals.Funds {
		v, err := q.FundBalance(ctx, ft.FundID)
		if err != nil {
			return fmt.Errorf("backup: recomputing fund %d balance: %w", ft.FundID, err)
		}
		if got := money.FromDB(v).Int64(); got != ft.Balance {
			return fmt.Errorf("%w: fund %d recomputed %d, file says %d", ErrTotalsMismatch, ft.FundID, got, ft.Balance)
		}
	}
	for _, at := range doc.Totals.Accounts {
		v, err := q.AccountBalance(ctx, store.AccountBalanceParams{FundID: accountFund[at.AccountID], AccountID: at.AccountID})
		if err != nil {
			return fmt.Errorf("backup: recomputing account %d balance: %w", at.AccountID, err)
		}
		if got := money.FromDB(v).Int64(); got != at.Balance {
			return fmt.Errorf("%w: account %d recomputed %d, file says %d", ErrTotalsMismatch, at.AccountID, got, at.Balance)
		}
	}
	for _, pt := range doc.Totals.Purposes {
		v, err := q.PurposeBalance(ctx, store.PurposeBalanceParams{FundID: purposeFund[pt.PurposeID], PurposeID: pt.PurposeID})
		if err != nil {
			return fmt.Errorf("backup: recomputing purpose %d balance: %w", pt.PurposeID, err)
		}
		if got := money.FromDB(v).Int64(); got != pt.Balance {
			return fmt.Errorf("%w: purpose %d recomputed %d, file says %d", ErrTotalsMismatch, pt.PurposeID, got, pt.Balance)
		}
	}
	return nil
}

// writeReceiptFileNamed writes data at uploadsDir/name, temp-file-then-
// rename like receipts.go's own writeReceiptFile - except under a name
// this package is handed, never one it mints itself: a restore's receipt
// keeps the exact filename its Receipt row already names (ADR-011's
// stored, server-generated name, preserved verbatim like every other id
// this package restores). "Additive" (ADR-012): an existing file already
// at that name is overwritten with what should be identical bytes -
// two receipts colliding on a crypto/rand name is not a case this
// package tries to detect.
//
// Callers must have already proven name is a bare filename (ParseUpload's
// own check) - this function does not re-check, the same trust boundary
// addReceiptToZip already documents for a live receipt row's path.
func writeReceiptFileNamed(uploadsDir, name string, data []byte) error {
	tmp, err := os.CreateTemp(uploadsDir, ".receipt-restore-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filepath.Join(uploadsDir, name)); err != nil {
		return err
	}
	removeTemp = false
	return nil
}
