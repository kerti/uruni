// restore.go is the restore half of ADR-012: parsing and validating an
// uploaded backup zip (ParseUpload), previewing what it would change
// (BuildPreview), and the restore itself, in two entry points over one core:
// Restore, the in-app one, and RestoreForRebuild, which `uruni rebuild` runs
// on a fresh database (cmd/uruni/rebuild.go). They are plain package functions
// because restore is one-shot with no state to carry.
//
// (A blank line separates this from `package backup` so revive does not read
// it as the package doc comment, which export.go owns.)

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
	// ever wrote is one, but a restore validates whatever the zip carries.
	_ "image/png" // registers the PNG decoder - decode-only, same reasoning
	// as receipt_image.go.
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp" // registers the WebP decoder - decode-only,
	// as receipt_image.go.

	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// Upload caps: 256 MB compressed (enforced by the handler's MaxBytesReader;
// the constant lives here so both layers read one value), 1 GB unzipped -
// counted from bytes actually copied, never an entry's declared size, which
// is what a zip bomb lies about - and 5,000 entries.
//
// var, not const, so restore_test.go can shrink them.
var (
	MaxUploadBytes   int64 = 256 << 20
	MaxUnzippedBytes int64 = 1 << 30
	MaxZipEntries          = 5000
)

// CreateRestoreStagingTemp opens a uniquely-named temp file inside backupDir
// for an inspect route to stream an upload (or a copied stored dump) into
// before it knows the file is a valid backup. The name is never shared, so
// two concurrent inspects (two tabs) cannot race over which bytes a rename
// leaves behind. restoreStage (internal/http/restore.go) owns the file once
// a successful inspect stages it; a failed inspect removes only this file.
//
// It uses dumps.go's tempPrefix, so a crash before restoreStage takes
// ownership leaves a stale temp that RemoveStaleTemps sweeps at boot. The
// prefix starts with "." and never "uruni-", so it cannot match
// dumpNamePattern: a staged file never appears in a backup listing, is never
// pruned by retention, and never perturbs the daily-dump change hash.
func CreateRestoreStagingTemp(backupDir string) (*os.File, error) {
	f, err := os.CreateTemp(backupDir, tempPrefix+"restore-*")
	if err != nil {
		return nil, fmt.Errorf("backup: creating a restore staging temp file in %s: %w", backupDir, err)
	}
	return f, nil
}

// MaxReceiptPixels bounds what a decoder may allocate (ADR-011's decode
// check). A byte cap bounds only compressed bytes: a ~100-byte PNG can
// declare 65000x65000 in its header and make image/png allocate gigabytes
// (x/image/webp has the same gap). 50 MP clears a 48 MP phone sensor and
// keeps one decode to a few hundred MB. It lives here because http already
// imports this package, so the receipt upload route and restore share one
// constant.
const MaxReceiptPixels = 50_000_000

// Sentinel errors from ParseUpload, BuildPreview and Restore, wrapped with
// the detail (a byte count, a table name, an id) that made each fire.
// Callers distinguish them with errors.Is.
var (
	// ErrFormatVersionNewer and ErrFormatVersionOlder are distinct: a newer file
	// means the binary needs upgrading first; an older one means this binary has
	// no step to bring it up to date (CanUpgradeFormat) - the previous release's
	// format always has one (ADR-012). Restoring onto an older binary is refused,
	// rolling the image back is the operator's job. Both refuse, for different
	// reasons a treasurer would act on differently.
	ErrFormatVersionNewer = errors.New("backup: the file was made by a newer version of Uruni than this server runs")
	ErrFormatVersionOlder = errors.New("backup: the file was made by an older version of Uruni than this server understands")
	// ErrMalformedBackup covers every shape problem short of a version
	// mismatch: not a zip, no uruni.json, an unknown field, or a receipt path
	// that is not a bare filename (a decoded backup is untrusted input, unlike a
	// live receipt row's server-generated path).
	ErrMalformedBackup  = errors.New("backup: the file is not a valid Uruni backup")
	ErrTooManyEntries   = errors.New("backup: the zip holds more files than a backup ever should")
	ErrUploadTooLarge   = errors.New("backup: the file is larger than a backup ever should be")
	ErrUnzippedTooLarge = errors.New("backup: the zip's contents are larger, unzipped, than a backup ever should be")
	// ErrBadReceiptImage is a receipt in the zip that fails the decode checks
	// ADR-011 applies on upload. A missing file is not this: ParseUpload skips
	// it silently (a missing image never blocks a restore).
	ErrBadReceiptImage = errors.New("backup: a receipt image in the file could not be read")
	// ErrTotalsMismatch is the refusal for a backup whose totals block
	// disagrees with its rows (compareTotals). Producers raise it before a byte
	// is written (Export, the dumps), ParseUpload at inspect, and Restore inside
	// the write transaction before COMMIT, which rolls back with nothing changed.
	ErrTotalsMismatch = errors.New("backup: the restored totals did not match the file's own totals")
	// ErrRebuildLogins is RestoreForRebuild's refusal of a backup that does not
	// hold exactly one login: zero would leave a populated instance open to
	// first registration (ADR-030), and two is not a shape this app produces.
	ErrRebuildLogins = errors.New("backup: a rebuild needs a backup that holds exactly one login")
)

// ParsedUpload is ParseUpload's result: the decoded, validated document and
// the on-disk path of the staged zip Restore re-opens to extract receipts,
// never the receipt bytes themselves (holding up to 1 GB of them for the
// 10-minute stage was a RAM bug). BuildPreview and Restore both take this.
//
// StagingPath is empty only when a test hand-builds a ParsedUpload whose
// Document has no Receipts; Restore never opens a zip it has nothing to
// extract from.
type ParsedUpload struct {
	Document    Document
	StagingPath string
}

// ParseUpload decodes and validates a backup zip already on disk at path
// (the inspect handler's staging file, internal/http/restore.go), so no
// caller hands it a whole zip in memory. Read-only: safe as the "inspect"
// step, before the treasurer has typed her password.
//
// Order matters: the entry and unzipped-byte caps are enforced while
// reading; format_version is checked next against the raw bytes, so a newer
// or older file gets its distinct error before DisallowUnknownFields sees a
// shape this server may not recognise; the strict decode runs once the
// version matches; then CheckTotals, so a self-contradicting document is
// refused with ErrTotalsMismatch before the password prompt (Restore proves
// the same totals again against the database it fills); and every receipt
// image is decode-checked last, one at a time, its bytes discarded after its
// check.
func ParseUpload(path string) (ParsedUpload, error) {
	//nolint:gosec // path is this package's staging file (internal/http/restore.go
	// streams the upload or stored dump into it) or a test fixture, never built
	// from request input.
	f, err := os.Open(path)
	if err != nil {
		return ParsedUpload{}, fmt.Errorf("%w: opening the staged upload: %v", ErrMalformedBackup, err) //nolint:errorlint
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return ParsedUpload{}, fmt.Errorf("%w: statting the staged upload: %v", ErrMalformedBackup, err) //nolint:errorlint
	}
	size := info.Size()
	if size > MaxUploadBytes {
		return ParsedUpload{}, fmt.Errorf("%w: %d bytes, more than the %d byte cap", ErrUploadTooLarge, size, MaxUploadBytes)
	}

	zr, err := zip.NewReader(f, size)
	if err != nil {
		return ParsedUpload{}, fmt.Errorf("%w: not a zip file: %v", ErrMalformedBackup, err) //nolint:errorlint // wrapping two errors in one Errorf is intentional here; ErrMalformedBackup is the one callers match against
	}
	if len(zr.File) > MaxZipEntries {
		return ParsedUpload{}, fmt.Errorf("%w: %d entries, more than the %d a backup ever holds", ErrTooManyEntries, len(zr.File), MaxZipEntries)
	}

	remaining := MaxUnzippedBytes

	// Pass 1: find uruni.json (not assumed first in the zip) and decode it.
	// Every matching entry is read and capped, so a crafted zip with two
	// entries of that name cannot dodge the unzipped-bytes cap.
	var jsonBytes []byte
	for _, zf := range zr.File {
		if zf.Name != jsonFilename {
			continue
		}
		data, n, err := readCappedEntry(zf, remaining)
		if err != nil {
			return ParsedUpload{}, err
		}
		remaining -= n
		jsonBytes = data
	}
	if jsonBytes == nil {
		return ParsedUpload{}, fmt.Errorf("%w: no %s entry", ErrMalformedBackup, jsonFilename)
	}

	version, err := checkFormatVersion(jsonBytes)
	if err != nil {
		return ParsedUpload{}, err
	}

	// An older file is decoded in its own shape and upgraded to the current one
	// here, so the restore, the inspect step and `uruni rebuild` only ever see the
	// current Document (upgrade.go).
	doc, err := decodeDocument(version, jsonBytes)
	if err != nil {
		return ParsedUpload{}, err
	}

	// Every fund's zone must be one of the three (ADR-041). The preview cannot
	// date the file without it, so a zone tz.Load refuses, unknown or the empty
	// string a missing field reads as, is refused here at inspect; the schema's
	// CHECK stays the backstop at Restore.
	for _, f := range doc.Funds {
		if _, err := tz.Load(f.TimeZone); err != nil {
			return ParsedUpload{}, fmt.Errorf("%w: fund %d's time_zone: %v", ErrMalformedBackup, f.ID, err) //nolint:errorlint
		}
	}

	// Every receipt path must be a bare filename before any zip entry is
	// trusted to match it: r.Path came from an uploaded, untrusted file, unlike
	// a live row's path (receipts.go only sets it from randomReceiptFilename).
	// The check depends only on doc.Receipts, not on pass 2.
	referenced := make(map[string]bool, len(doc.Receipts))
	for _, r := range doc.Receipts {
		if r.Path != filepath.Base(r.Path) {
			return ParsedUpload{}, fmt.Errorf("%w: receipt path %q is not a bare filename", ErrMalformedBackup, r.Path)
		}
		referenced[r.Path] = true
	}

	// The document must agree with itself (#496): its totals block, recomputed
	// from its transaction rows. A file torn by a write mid-export, or edited by
	// hand, is refused here at inspect, before the password prompt. Restore
	// proves the same totals again (verifyTotals).
	if err := CheckTotals(doc); err != nil {
		return ParsedUpload{}, err
	}

	// Pass 2: every other entry, read and capped exactly once, one at a time,
	// never alongside another entry's bytes. An entry doc.Receipts references
	// is decode-checked on the same bytes before they are discarded; an
	// unreferenced entry is still read (so the unzipped-bytes cap bounds it) but
	// never validated or kept.
	for _, zf := range zr.File {
		if zf.Name == jsonFilename {
			continue
		}
		data, n, err := readCappedEntry(zf, remaining)
		if err != nil {
			return ParsedUpload{}, err
		}
		remaining -= n

		if !strings.HasPrefix(zf.Name, receiptsDir) {
			continue
		}
		name := strings.TrimPrefix(zf.Name, receiptsDir)
		if !referenced[name] {
			continue // ADR-012 ruling 5 works the other way too: a zip entry no row names is never used for anything
		}
		if err := validateReceiptImageBytes(data); err != nil {
			return ParsedUpload{}, err
		}
	}

	return ParsedUpload{Document: doc, StagingPath: path}, nil
}

// readCappedEntry reads one zip entry fully, refusing to read past
// remaining+1 bytes whatever the header claims: the read, not the header, is
// what a zip bomb has to lie through.
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

// checkFormatVersion peeks format_version out of the raw JSON before the
// strict decode, so a file from a differently-shaped future version gets
// the "newer version" refusal instead of a generic DisallowUnknownFields
// error, and returns it for decodeDocument to pick the shape. A version the
// importer has no step for (CanUpgradeFormat) is the "older version" refusal.
func checkFormatVersion(jsonBytes []byte) (int64, error) {
	var peek struct {
		FormatVersion int64 `json:"format_version"`
	}
	if err := json.Unmarshal(jsonBytes, &peek); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrMalformedBackup, err) //nolint:errorlint
	}
	switch {
	case peek.FormatVersion > FormatVersion:
		return 0, fmt.Errorf("%w: file is format_version %d, this server understands %d", ErrFormatVersionNewer, peek.FormatVersion, FormatVersion)
	case !CanUpgradeFormat(peek.FormatVersion):
		return 0, fmt.Errorf("%w: file is format_version %d, this server understands %d", ErrFormatVersionOlder, peek.FormatVersion, FormatVersion)
	default:
		return peek.FormatVersion, nil
	}
}

// validateReceiptImageBytes is ADR-011's decode check: the image must decode
// within the pixel ceiling receipt_image.go enforces on upload. It does not
// downscale or re-encode; Restore writes these bytes to the uploads volume
// verbatim.
func validateReceiptImageBytes(data []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBadReceiptImage, err) //nolint:errorlint
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxReceiptPixels {
		return fmt.Errorf("%w: %dx%d exceeds the pixel cap", ErrBadReceiptImage, cfg.Width, cfg.Height)
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("%w: %v", ErrBadReceiptImage, err) //nolint:errorlint
	}
	return nil
}

// FundStatus is one fund's place in a restore preview: on both sides, only
// live, or only in the file (ADR-012's per-fund confirm-step copy).
type FundStatus string

// The three shapes a whole-instance replace can produce for one fund.
const (
	FundKept    FundStatus = "kept"
	FundRemoved FundStatus = "removed"
	FundAdded   FundStatus = "added"
)

// FundPreview is one line of the confirm step's per-fund list.
// TransactionsLost and CutoffDate are meaningful only when Status is
// FundKept (ADR-012 restore scope): live transactions whose id is past the
// file's highest id, e.g. '12 transaksi setelah 26 Sep akan hilang'.
// CutoffDate is empty when the file has no transaction for this fund, so
// every live transaction counts as lost.
type FundPreview struct {
	FundID           int64      `json:"fund_id"`
	Name             string     `json:"name"`
	Status           FundStatus `json:"status"`
	TransactionsLost int64      `json:"transactions_lost"`
	CutoffDate       string     `json:"cutoff_date,omitempty"`
}

// Preview is BuildPreview's whole result: what the confirm step shows before
// the treasurer types her password.
type Preview struct {
	// Date is when the backup was taken: the newest created_at (or, for a
	// receipt, uploaded_at) in the file. Document carries no "exported at"
	// field (see backup.go), and a filename is not safe: a treasurer can rename
	// the file before re-uploading it.
	Date  string        `json:"date"`
	Total int64         `json:"total"`
	Funds []FundPreview `json:"funds"`
}

// BuildPreview compares doc against the live database and reports what a
// restore would change. Read-only, no transaction.
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

	date, err := newestMoment(doc)
	if err != nil {
		return Preview{}, err
	}
	preview := Preview{Date: date, Total: total.Int64(), Funds: []FundPreview{}}

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

// newestMoment returns the latest timestamp Document carries (created_at, or
// a receipt's uploaded_at) as a calendar date in the file's own zone (the first
// fund's, by the rule resolveFund applies; tz.Default for a file with no fund);
// see Preview.Date. A zone tz.Load refuses is an error: ParseUpload has refused
// such a file already, so this guards a Document built some other way.
func newestMoment(doc Document) (string, error) {
	zone := tz.Default
	if len(doc.Funds) > 0 {
		zone = doc.Funds[0].TimeZone
	}
	loc, err := tz.Load(zone)
	if err != nil {
		return "", fmt.Errorf("backup: dating the file: %w", err)
	}

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
	for _, x := range doc.MemberTiers {
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
		return "", nil
	}
	return time.Unix(latest, 0).In(loc).Format("2006-01-02"), nil
}

// immutableTriggers names 00001_schema.sql's eight BEFORE UPDATE/DELETE
// triggers guarding "transaction", transfer, reconciliation and
// reconciliation_line. Restore drops them for the width of its write
// transaction (SQLite cannot disable a trigger short of dropping it), clears
// and re-populates the four tables, then recreates each before COMMIT.
// ADR-004's single connection means nothing else can be in flight meanwhile.
//
// Only the names live here. Each body is read back from sqlite_master in the
// same transaction just before the drop, so an edit to a trigger in the
// migration can never be reverted by a stale copy here.
// TestRestoreRecreatesImmutableTriggers proves each one still refuses.
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

// userMode is what a restore does with the "user" table. A named type, not a
// bool: the two entry points differ in exactly this, and the call site should
// say which one it is.
type userMode int

const (
	// keepLiveUser is the in-app restore: the live login is left untouched
	// (ADR-012 "Login is not restored"), so the treasurer cannot be locked out
	// by a stale password hash.
	keepLiveUser userMode = iota
	// carryBackupUser is the rebuild: the target is a fresh database with no
	// login, and the backup's one login is inserted with its id.
	carryBackupUser
)

// Restore is the in-app write side of ADR-012's restore path: a safety-net
// dump, additive receipt extraction, then one write transaction that clears
// every fund-scoped table, re-inserts exactly what parsed.Document holds with
// every id preserved, proves the totals, and commits - or changes nothing. The
// live login is not touched; every session is cleared.
//
// sqlDB is the raw pool because this operation opens a transaction spanning
// nearly every table, and ledger.Ledger's balance methods are bound to
// sqlDB, not the open transaction: calling them here would ask ADR-004's
// single connection for a second one and deadlock. verifyTotals calls the
// generated FundBalance/AccountBalance/PurposeBalance directly on this
// transaction instead.
//
// WritePreRestoreDump runs before the transaction opens, reads the live
// state in its own read transaction (BuildDocument), and releases the
// connection before the write transaction asks for it.
func Restore(ctx context.Context, sqlDB *sql.DB, uploadsDir, backupDir string, parsed ParsedUpload, now time.Time) error {
	if _, err := WritePreRestoreDump(ctx, sqlDB, uploadsDir, backupDir, now); err != nil {
		return fmt.Errorf("backup: writing the safety-net dump: %w", err)
	}
	_, err := restoreCore(ctx, sqlDB, uploadsDir, parsed, keepLiveUser)
	return err
}

// CheckRebuildLogin is the read-only half of RestoreForRebuild's login rule,
// so `uruni rebuild` can refuse before it writes anything.
func CheckRebuildLogin(doc Document) error {
	if len(doc.Users) != 1 {
		return fmt.Errorf("%w: this one holds %d", ErrRebuildLogins, len(doc.Users))
	}
	return nil
}

// RestoreForRebuild is the second entry point: it fills a freshly migrated,
// empty database (the scratch file `uruni rebuild` builds) from a backup,
// login included. No safety-net dump: nothing live is being replaced, the old
// file stays where it is until the rebuild swaps. The same totals proof runs
// before COMMIT. The login is carried over because the lockout reasoning of
// "Login is not restored" does not apply: there is no live password to
// preserve, and a populated instance with no login would be open to first
// registration (ADR-030).
//
// It returns the receipt files it created in uploadsDir (not ones already on
// disk). On error it has already removed them; on success the caller removes
// them with RemoveCreatedReceipts if a later step of the rebuild fails.
func RestoreForRebuild(ctx context.Context, sqlDB *sql.DB, uploadsDir string, parsed ParsedUpload) (created []string, err error) {
	if err := CheckRebuildLogin(parsed.Document); err != nil {
		return nil, err
	}
	return restoreCore(ctx, sqlDB, uploadsDir, parsed, carryBackupUser)
}

// RemoveCreatedReceipts deletes the files RestoreForRebuild reported, best
// effort (see removeFiles).
func RemoveCreatedReceipts(paths []string) { removeFiles(paths) }

// restoreCore is what both entry points share: additive receipt extraction,
// then the one write transaction. A restore that fails removes the receipt
// files it created, so a refused restore changes nothing on disk but what the
// caller wrote before it.
func restoreCore(ctx context.Context, sqlDB *sql.DB, uploadsDir string, parsed ParsedUpload, users userMode) (created []string, err error) {
	// Receipt files this restore created (not ones already on disk under the
	// same name) are removed if anything fails before the commit.
	written, err := extractReceipts(uploadsDir, parsed.Document, parsed.StagingPath)
	committed := false
	defer func() {
		if !committed {
			removeFiles(written)
		}
	}()
	if err != nil {
		return nil, err
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("backup: beginning the restore transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has run

	if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("backup: setting defer_foreign_keys: %w", err)
	}

	triggerSQL := make([]string, len(immutableTriggers))
	for i, name := range immutableTriggers {
		if err := tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?", name).Scan(&triggerSQL[i]); err != nil {
			return nil, fmt.Errorf("backup: reading trigger %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, "DROP TRIGGER "+name); err != nil {
			return nil, fmt.Errorf("backup: dropping trigger %s: %w", name, err)
		}
	}

	tq := store.New(tx)

	if err := clearFundScopedTables(ctx, tq); err != nil {
		return nil, err
	}
	if users == carryBackupUser {
		// No clearing first: the target must hold no login. Checked, not left to a
		// unique-email or primary-key clash, which a login with another id and
		// email would not trip.
		if n, err := tq.CountUsers(ctx); err != nil {
			return nil, fmt.Errorf("backup: counting logins: %w", err)
		} else if n != 0 {
			return nil, fmt.Errorf("%w: the target database already has %d login(s)", ErrRebuildLogins, n)
		}
		u := parsed.Document.Users[0]
		if err := tq.RestoreUser(ctx, store.RestoreUserParams{ID: u.ID, Email: u.Email, PasswordHash: u.PasswordHash, CreatedAt: u.CreatedAt}); err != nil {
			return nil, fmt.Errorf("backup: carrying the login over: %w", err)
		}
	}
	if err := insertDocument(ctx, tq, parsed.Document); err != nil {
		return nil, err
	}

	for i, name := range immutableTriggers {
		if _, err := tx.ExecContext(ctx, triggerSQL[i]); err != nil {
			return nil, fmt.Errorf("backup: recreating trigger %s: %w", name, err)
		}
	}

	// Every session is gone after a restore (ADR-012: sessions are never
	// exported, a restore logs everyone out), inside the same transaction, so a
	// rolled-back restore leaves the treasurer's session untouched.
	if err := tq.DeleteAllSessions(ctx); err != nil {
		return nil, fmt.Errorf("backup: clearing sessions: %w", err)
	}

	if err := verifyTotals(ctx, tq, parsed.Document); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("backup: committing the restore: %w", err)
	}
	committed = true
	return written, nil
}

// clearFundScopedTables deletes every row from every table this package
// restores, except "user" (ADR-012's login ruling) and "session" (cleared in
// restoreCore). Listed child-before-parent for a reader; order does not matter
// because defer_foreign_keys=ON defers every FK check to COMMIT.
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
		{"member_tier", q.DeleteAllMemberTiers},
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

// insertDocument writes every row doc holds in Document's field order (the
// migration's dependency order, see backup.go), except doc.Transactions,
// which goes through restoreTransactionsInOrder: the transaction_named_row_shape
// trigger (ADR-012 obligation 1) checks at INSERT that a reversal's target
// exists and that the reversal copies it the other way (ADR-042), and no PRAGMA
// defers that. Other foreign keys are declarative and
// land in any order under defer_foreign_keys=ON.
func insertDocument(ctx context.Context, q store.Querier, doc Document) error {
	for _, f := range doc.Funds {
		if err := q.RestoreFund(ctx, store.RestoreFundParams{
			ID: f.ID, Name: f.Name, Currency: f.Currency, TimeZone: f.TimeZone, ReportSlug: f.ReportSlug, CreatedAt: f.CreatedAt,
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
			ID: dr.ID, TierID: dr.TierID, Amount: dr.Amount, EffectiveFrom: dr.EffectiveFrom,
			Correction: dr.Correction, Deleted: boolToInt(dr.Deleted), CreatedAt: dr.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring dues rate %d: %w", dr.ID, err)
		}
	}
	for _, m := range doc.Members {
		if err := q.RestoreMember(ctx, store.RestoreMemberParams{
			ID: m.ID, FundID: m.FundID, Name: m.Name,
			JoinedOn: m.JoinedOn, InactiveOn: m.InactiveOn, CreatedAt: m.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring member %d: %w", m.ID, err)
		}
	}
	for _, mt := range doc.MemberTiers {
		if err := q.RestoreMemberTier(ctx, store.RestoreMemberTierParams{
			ID: mt.ID, FundID: mt.FundID, MemberID: mt.MemberID, TierID: mt.TierID,
			EffectiveFrom: mt.EffectiveFrom, Correction: mt.Correction, CreatedAt: mt.CreatedAt,
		}); err != nil {
			return fmt.Errorf("backup: restoring member tier %d: %w", mt.ID, err)
		}
	}
	for _, tr := range doc.Transfers {
		if err := q.RestoreTransfer(ctx, store.RestoreTransferParams{
			ID: tr.ID, FundID: tr.FundID, Kind: tr.Kind, CorrectsTransactionID: tr.CorrectsTransactionID, Reason: tr.Reason, CreatedAt: tr.CreatedAt,
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

// restoreTransactionsInOrder inserts every row in txns once the row its
// reverses_transaction_id names (if any) is inserted: the one dependency the
// transaction_named_row_shape trigger enforces immediately (ADR-012
// obligation 1: by dependency, not by id, since id order is not something
// new code may rely on). A recursive insert-then-mark-visited walk, not a
// sort by id, because the file's array order must not be assumed to be id
// order (CLAUDE.md: no primary-key order).
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
			// Names a row this file does not carry. Nothing to insert on its behalf;
			// if another row depends on it, the trigger refuses that insert.
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

// verifyTotals is ADR-012's "a restore proves its numbers": every fund,
// account and purpose doc lists, recomputed by the generated balance queries
// from the rows Restore just inserted, in the same open transaction, and
// compared exactly (int64, no tolerance) with doc.Totals through
// compareTotals, the comparison the inspect step and every dump share. It
// calls the generated queries directly, not a *ledger.Ledger; see Restore
// for why.
func verifyTotals(ctx context.Context, q store.Querier, doc Document) error {
	got := Totals{
		Funds:    make([]FundTotal, 0, len(doc.Funds)),
		Accounts: make([]AccountTotal, 0, len(doc.Accounts)),
		Purposes: make([]PurposeTotal, 0, len(doc.Purposes)),
	}
	for _, f := range doc.Funds {
		v, err := q.FundBalance(ctx, f.ID)
		if err != nil {
			return fmt.Errorf("backup: recomputing fund %d balance: %w", f.ID, err)
		}
		got.Funds = append(got.Funds, FundTotal{FundID: f.ID, Balance: money.FromDB(v).Int64()})
	}
	for _, a := range doc.Accounts {
		v, err := q.AccountBalance(ctx, store.AccountBalanceParams{FundID: a.FundID, AccountID: a.ID})
		if err != nil {
			return fmt.Errorf("backup: recomputing account %d balance: %w", a.ID, err)
		}
		got.Accounts = append(got.Accounts, AccountTotal{AccountID: a.ID, Balance: money.FromDB(v).Int64()})
	}
	for _, p := range doc.Purposes {
		v, err := q.PurposeBalance(ctx, store.PurposeBalanceParams{FundID: p.FundID, PurposeID: p.ID})
		if err != nil {
			return fmt.Errorf("backup: recomputing purpose %d balance: %w", p.ID, err)
		}
		got.Purposes = append(got.Purposes, PurposeTotal{PurposeID: p.ID, Balance: money.FromDB(v).Int64()})
	}
	return compareTotals(doc.Totals, got)
}

// extractReceipts re-opens the staged zip at stagingPath once and streams
// each receipt doc names into uploadsDir, one at a time, never loading a
// receipt into a map first. A Document with no Receipts never opens
// stagingPath, so a hand-built ParsedUpload in tests may leave it empty.
func extractReceipts(uploadsDir string, doc Document, stagingPath string) (written []string, err error) {
	if len(doc.Receipts) == 0 {
		return nil, nil
	}

	//nolint:gosec // stagingPath is this package's staging file
	// (internal/http/restore.go) or a test fixture, never request input.
	f, err := os.Open(stagingPath)
	if err != nil {
		return nil, fmt.Errorf("backup: re-opening the staged upload to extract receipts: %w", err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("backup: statting the staged upload: %w", err)
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return nil, fmt.Errorf("backup: re-opening the staged upload as a zip: %w", err)
	}
	byName := make(map[string]*zip.File, len(zr.File))
	for _, zf := range zr.File {
		byName[zf.Name] = zf
	}

	for _, r := range doc.Receipts {
		zf, ok := byName[receiptsDir+r.Path]
		if !ok {
			continue // ruling 5: a missing image never blocks a restore
		}
		created, err := writeReceiptFileFromZip(uploadsDir, r.Path, zf)
		if err != nil {
			return written, fmt.Errorf("backup: extracting receipt %s: %w", r.Path, err)
		}
		if created {
			written = append(written, filepath.Join(uploadsDir, r.Path))
		}
	}
	return written, nil
}

// removeFiles deletes each path, best effort: it runs only on a restore that
// has already failed, and a file it cannot remove is an orphan ADR-011
// tolerates, not a reason to mask the original error.
func removeFiles(paths []string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// writeReceiptFileFromZip streams one receipt's zip entry into
// uploadsDir/name through WriteFileAtomic (temp file, fsync, rename, fsync
// the directory), like receipts.go's writeReceiptFile, except under the
// filename the Receipt row already names (ADR-011's server-generated name,
// preserved verbatim). "Additive" (ADR-012): an existing file at that name is
// overwritten with what should be identical bytes; a crypto/rand name
// collision is not detected.
//
// The copy re-applies MaxUnzippedBytes as a per-entry ceiling while reading,
// never trusting zf's declared UncompressedSize64 (see readCappedEntry).
// ParseUpload already proved the entry decodes; this only copies bytes.
//
// Callers must have proven name is a bare filename (ParseUpload does); this
// does not re-check, the same trust boundary addReceiptToZip documents.
func writeReceiptFileFromZip(uploadsDir, name string, zf *zip.File) (created bool, err error) {
	rc, err := zf.Open()
	if err != nil {
		return false, fmt.Errorf("opening %s in the staged zip: %w", zf.Name, err)
	}
	defer func() { _ = rc.Close() }()

	dest := filepath.Join(uploadsDir, name)
	_, statErr := os.Stat(dest)
	err = WriteFileAtomic(uploadsDir, ".receipt-restore-*.tmp", name, func(w io.Writer) error {
		n, err := io.Copy(w, io.LimitReader(rc, MaxUnzippedBytes+1))
		if err != nil {
			return err
		}
		if n > MaxUnzippedBytes {
			return fmt.Errorf("%w: more than %d bytes unzipped", ErrUnzippedTooLarge, MaxUnzippedBytes)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return statErr != nil, nil
}

// boolToInt is the stored form of a boolean column (0 or 1).
func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
