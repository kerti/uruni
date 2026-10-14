package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/lock"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// Tests for #516: `uruni rebuild`. Every test builds its own directory and
// drives runRebuild directly with a fixed clock and version, so none touches
// the process environment and all can run in parallel.

const (
	rebuildTestVersion  = "v9.9.9"
	rebuildTestEmail    = "bendahara@rebuild.test"
	rebuildTestPassword = "a-long-enough-password"
	// The fixture fund: 1,000,000 opening + 250,000 in - 100,000 out.
	rebuildFixtureTotal = 1_150_000
	rebuildReceiptName  = "00000000000000000000000000rebuild.jpg"
)

// rebuildNow is the fixed clock; PreRebuildName renders it as 20261014-101500.
var rebuildNow = time.Date(2026, 10, 14, 10, 15, 0, 0, tz.Jakarta)

type rebuildFixture struct {
	dir        string
	dbPath     string
	backupDir  string
	uploadsDir string
	zipPath    string
	out        *bytes.Buffer
}

// newRebuildFixture makes a directory with a backup directory, an empty
// uploads directory and no database yet.
func newRebuildFixture(t *testing.T) *rebuildFixture {
	t.Helper()
	dir := t.TempDir()
	f := &rebuildFixture{
		dir:        dir,
		dbPath:     filepath.Join(dir, "data", "uruni.db"),
		backupDir:  filepath.Join(dir, "backups"),
		uploadsDir: filepath.Join(dir, "uploads"),
		zipPath:    filepath.Join(dir, "backup.zip"),
		out:        &bytes.Buffer{},
	}
	for _, d := range []string{filepath.Dir(f.dbPath), f.backupDir, f.uploadsDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *rebuildFixture) run(hooks backup.ReplaceHooks, now time.Time) error {
	return runRebuild(context.Background(), rebuildRun{
		cfg:     config.Config{DBPath: f.dbPath, BackupDir: f.backupDir, UploadsDir: f.uploadsDir},
		zipPath: f.zipPath,
		version: rebuildTestVersion,
		now:     now,
		out:     f.out,
		logger:  quietLogger(),
		hooks:   hooks,
	})
}

func (f *rebuildFixture) asidePath() string {
	return filepath.Join(filepath.Dir(f.dbPath), backup.PreRebuildName(f.dbPath, rebuildTestVersion, rebuildNow))
}

// seedInstance writes a populated database of this binary's schema at path,
// with a receipt file in uploadsDir: a login, one fund, and three ledger rows
// summing to rebuildFixtureTotal.
func seedInstance(t *testing.T, path, uploadsDir string) {
	t.Helper()
	ctx := context.Background()
	sqlDB, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if _, err := db.Up(ctx, sqlDB, quietLogger()); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.New(sqlDB).Register(ctx, rebuildTestEmail, rebuildTestPassword); err != nil {
		t.Fatal(err)
	}
	l := ledger.New(sqlDB)
	setup, err := l.SetUpFund(ctx, ledger.SetUpFundParams{
		FundName: "Kas Uji Rebuild",
		Accounts: []ledger.AccountInput{{
			Kind: "cash", Name: "Tunai",
			OpeningBalance: &ledger.OpeningBalance{Amount: money.Amount(1_000_000), OccurredOn: "2026-10-01"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	post := func(direction string, amount int64) store.Transaction {
		txn, err := l.PostTransaction(ctx, ledger.PostTransactionParams{
			FundID: setup.Fund.ID, AccountID: setup.Accounts[0].ID, PurposeID: setup.MainPurposeID,
			Direction: direction, Amount: money.Amount(amount), OccurredOn: "2026-10-05",
		})
		if err != nil {
			t.Fatal(err)
		}
		return txn
	}
	in := post("in", 250_000)
	post("out", 100_000)

	if _, err := store.New(sqlDB).CreateReceipt(ctx, store.CreateReceiptParams{
		FundID: setup.Fund.ID, TransactionID: &in.ID, Path: rebuildReceiptName, UploadedAt: 1_700_000_000,
	}); err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploadsDir, rebuildReceiptName), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// exportZip writes the database's backup to zipPath, as the old binary's
// in-app download would have.
func exportZip(t *testing.T, dbPath, uploadsDir, zipPath string) {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	data, _, err := backup.Export(context.Background(), sqlDB, uploadsDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zipPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// execRaw runs statements against the database file directly.
func execRaw(t *testing.T, path string, stmts ...string) {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	for _, s := range stmts {
		if _, err := sqlDB.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// makeOld turns a current-schema database into an old-schema one: member gains
// a column this binary's schema does not have, so CheckSchema differs while
// every row stays.
func makeOld(t *testing.T, path string) {
	t.Helper()
	execRaw(t, path, `ALTER TABLE member ADD COLUMN legacy_note TEXT`)
}

// oldInstance is the usual starting point: an old-schema database at dbPath and
// the backup of it at zipPath.
func oldInstance(t *testing.T) *rebuildFixture {
	t.Helper()
	f := newRebuildFixture(t)
	seedInstance(t, f.dbPath, f.uploadsDir)
	exportZip(t, f.dbPath, f.uploadsDir, f.zipPath)
	makeOld(t, f.dbPath)
	// A fresh, empty uploads volume for the rebuild itself.
	f.uploadsDir = filepath.Join(f.dir, "uploads-new")
	if err := os.Mkdir(f.uploadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return f
}

func copyFileTo(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src) //nolint:gosec // a test's own temp path
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil { //nolint:gosec // a test's own temp path
		t.Fatal(err)
	}
}

// rewriteBackup decodes the zip's uruni.json, lets edit change it, and writes
// the zip back with the receipts untouched. Totals are not recomputed: a test
// that needs them consistent keeps them so.
func rewriteBackup(t *testing.T, zipPath string, edit func(*backup.Document)) {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, zf := range zr.File {
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if zf.Name == "uruni.json" {
			var doc backup.Document
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			edit(&doc)
			if data, err = json.Marshal(doc); err != nil {
				t.Fatal(err)
			}
		}
		w, err := zw.Create(zf.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = zr.Close()
	if err := os.WriteFile(zipPath, out.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a test's own temp path
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// assertUntouched is what every refusal owes: uruni.db is byte for byte what it
// was, and nothing the rebuild writes is left behind.
func assertUntouched(t *testing.T, f *rebuildFixture, before []byte) {
	t.Helper()
	if after := readFile(t, f.dbPath); !bytes.Equal(before, after) {
		t.Errorf("uruni.db changed (%d bytes before, %d after), want it untouched", len(before), len(after))
	}
	for _, e := range dirNames(t, filepath.Dir(f.dbPath)) {
		if strings.HasPrefix(e, backup.RebuildScratchPrefix) {
			t.Errorf("scratch file %s left behind", e)
		}
	}
}

// assertNothingWritten adds, for a refusal from the read-only checks: no aside
// copy, no snapshot, no marker.
func assertNothingWritten(t *testing.T, f *rebuildFixture, before []byte) {
	t.Helper()
	assertUntouched(t, f, before)
	for _, e := range dirNames(t, filepath.Dir(f.dbPath)) {
		if strings.Contains(e, ".pre-") {
			t.Errorf("%s was written by a rebuild that was refused by the read-only checks", e)
		}
	}
	if names := dirNames(t, f.backupDir); len(names) != 0 {
		t.Errorf("backup directory holds %v after a refused rebuild, want nothing (no snapshot, no marker)", names)
	}
}

// openRebuilt opens the swapped-in database and fails the test unless it is
// this binary's schema.
func openRebuilt(t *testing.T, path string) *sql.DB {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	diff, err := db.CheckSchema(context.Background(), sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Matches() {
		t.Fatalf("rebuilt schema differs: %s", diff.Summary(5))
	}
	return sqlDB
}

func TestRebuildReplacesAnOldSchemaDatabaseFromABackup(t *testing.T) {
	t.Parallel()
	f := oldInstance(t)
	if err := os.Chmod(f.dbPath, 0o640); err != nil { //nolint:gosec // a mode other than the default is the point: the rebuild must keep it
		t.Fatal(err)
	}
	oldBytes := readFile(t, f.dbPath)

	if err := f.run(backup.ReplaceHooks{}, rebuildNow); err != nil {
		t.Fatalf("rebuild = %v, want no error", err)
	}

	sqlDB := openRebuilt(t, f.dbPath)
	ctx := context.Background()
	users, err := store.New(sqlDB).ListUsers(ctx)
	if err != nil || len(users) != 1 || users[0].Email != rebuildTestEmail {
		t.Fatalf("ListUsers = (%v, %v), want the one carried-over login %s", users, err, rebuildTestEmail)
	}
	if _, err := auth.New(sqlDB).Authenticate(ctx, rebuildTestEmail, rebuildTestPassword); err != nil {
		t.Errorf("the carried-over login does not accept the old password: %v", err)
	}
	funds, err := store.New(sqlDB).ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("ListFunds = (%v, %v), want one fund", funds, err)
	}
	total, err := ledger.New(sqlDB).FundBalance(ctx, funds[0].ID)
	if err != nil || total.Int64() != rebuildFixtureTotal {
		t.Errorf("FundBalance = (%d, %v), want %d", total.Int64(), err, rebuildFixtureTotal)
	}
	if _, err := os.Stat(filepath.Join(f.uploadsDir, rebuildReceiptName)); err != nil {
		t.Errorf("the receipt was not extracted into the uploads volume: %v", err)
	}

	// The old file is kept, byte for byte, under the documented name.
	wantAside := filepath.Join(filepath.Dir(f.dbPath), "uruni.db.pre-v9.9.9-20261014-101500")
	if f.asidePath() != wantAside {
		t.Fatalf("aside name = %s, want %s", f.asidePath(), wantAside)
	}
	if kept := readFile(t, wantAside); !bytes.Equal(kept, oldBytes) {
		t.Errorf("the kept copy differs from the old database")
	}
	if info, err := os.Stat(f.dbPath); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("new database mode = %v (%v), want the old file's 0640", info.Mode().Perm(), err)
	}

	// Nothing the rebuild writes is left over, and the old sidecars are gone.
	for _, e := range dirNames(t, filepath.Dir(f.dbPath)) {
		if strings.HasPrefix(e, backup.RebuildScratchPrefix) {
			t.Errorf("scratch file %s left behind", e)
		}
	}

	// The #517 guard ran (a snapshot of the old file, a marker that says it was
	// taken) but a rebuild never records a boot.
	snaps, err := backup.ListPreUpgradeSnapshots(f.backupDir)
	if err != nil || len(snaps) != 1 || snaps[0].To != rebuildTestVersion {
		t.Errorf("snapshots = (%v, %v), want one taken for %s", snaps, err, rebuildTestVersion)
	}
	marker := markerText(t, f.backupDir)
	if !strings.Contains(marker, "snapshotted "+rebuildTestVersion) || strings.Contains(marker, "booted") {
		t.Errorf("marker = %q, want snapshotted %s and no booted line", marker, rebuildTestVersion)
	}

	out := f.out.String()
	for _, want := range []string{
		"funds: Kas Uji Rebuild",
		"backup says   Rp 1.150.000",
		"rebuilt says  Rp 1.150.000",
		"old database  Rp 1.150.000",
		"not compared:",
		"login carried over: " + rebuildTestEmail,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for i := 0; i < len(out); i++ {
		if out[i] >= 0x80 {
			t.Fatalf("output has a non-ASCII byte at %d", i)
		}
	}
}

func TestRebuildOnAnAbsentDatabaseSkipsTheGate(t *testing.T) {
	t.Parallel()
	f := oldInstance(t)
	if err := os.Remove(f.dbPath); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"-wal", "-shm"} {
		_ = os.Remove(f.dbPath + s)
	}

	if err := f.run(backup.ReplaceHooks{}, rebuildNow); err != nil {
		t.Fatalf("rebuild on an absent database = %v, want no error", err)
	}
	sqlDB := openRebuilt(t, f.dbPath)
	if n, err := store.New(sqlDB).CountUsers(context.Background()); err != nil || n != 1 {
		t.Errorf("CountUsers = (%d, %v), want the carried-over login", n, err)
	}
	if !strings.Contains(f.out.String(), "there is no database") {
		t.Errorf("output does not say the gate was skipped:\n%s", f.out.String())
	}
	for _, e := range dirNames(t, filepath.Dir(f.dbPath)) {
		if strings.Contains(e, ".pre-") {
			t.Errorf("%s kept aside although there was no old database", e)
		}
	}
	if names := dirNames(t, f.backupDir); len(names) != 0 {
		t.Errorf("backup directory = %v, want nothing: there was nothing to snapshot", names)
	}
}

func TestRebuildRefusesWhileTheInstanceLockIsHeld(t *testing.T) {
	t.Parallel()
	f := oldInstance(t)
	before := readFile(t, f.dbPath)
	held, err := lock.Acquire(lock.PathFor(f.dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()

	err = f.run(backup.ReplaceHooks{}, rebuildNow)
	if !errors.Is(err, ErrServerRunning) {
		t.Fatalf("rebuild under a held lock = %v, want ErrServerRunning", err)
	}
	assertNothingWritten(t, f, before)
}

func TestRebuildRefusesADatabaseThatAlreadyHasThisSchema(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t)
	seedInstance(t, f.dbPath, f.uploadsDir)
	exportZip(t, f.dbPath, f.uploadsDir, f.zipPath)
	before := readFile(t, f.dbPath)

	err := f.run(backup.ReplaceHooks{}, rebuildNow)
	if !errors.Is(err, ErrSchemaCurrent) {
		t.Fatalf("rebuild on a current database = %v, want ErrSchemaCurrent", err)
	}
	if !strings.Contains(err.Error(), "Pengaturan") {
		t.Errorf("refusal = %q, want it to point at restoring in the app", err)
	}
	assertNothingWritten(t, f, before)
}

func TestRebuildRefusesAStaleBackup(t *testing.T) {
	t.Parallel()

	t.Run("the database has entries the backup lacks", func(t *testing.T) {
		t.Parallel()
		f := oldInstance(t)
		// An entry made after the backup was taken: member is not immutable.
		execRaw(t, f.dbPath, `INSERT INTO member (fund_id, name, joined_on, created_at) VALUES (1, 'Warga Baru', '2026-10-10', 1)`)
		before := readFile(t, f.dbPath)

		err := f.run(backup.ReplaceHooks{}, rebuildNow)
		if !errors.Is(err, ErrStaleBackup) {
			t.Fatalf("rebuild = %v, want ErrStaleBackup", err)
		}
		if !strings.Contains(err.Error(), "member: the database has 1, the backup 0") || !strings.Contains(err.Error(), "would be lost") {
			t.Errorf("refusal = %q, want the member counts and what would be lost", err)
		}
		assertNothingWritten(t, f, before)
	})

	t.Run("the backup has entries the database lacks", func(t *testing.T) {
		t.Parallel()
		f := newRebuildFixture(t)
		seedInstance(t, f.dbPath, f.uploadsDir)
		// The database is the earlier state; the backup was taken after a member
		// was added to a copy of it.
		later := filepath.Join(f.dir, "later.db")
		copyFileTo(t, f.dbPath, later)
		execRaw(t, later, `INSERT INTO member (fund_id, name, joined_on, created_at) VALUES (1, 'Warga Baru', '2026-10-10', 1)`)
		exportZip(t, later, f.uploadsDir, f.zipPath)
		makeOld(t, f.dbPath)
		before := readFile(t, f.dbPath)

		err := f.run(backup.ReplaceHooks{}, rebuildNow)
		if !errors.Is(err, ErrStaleBackup) {
			t.Fatalf("rebuild = %v, want ErrStaleBackup", err)
		}
		if !strings.Contains(err.Error(), "member: the database has 0, the backup 1") {
			t.Errorf("refusal = %q, want the member counts", err)
		}
		assertNothingWritten(t, f, before)
	})
}

func TestRebuildRefusesAnotherInstancesBackup(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*backup.Document){
		"a different report link": func(d *backup.Document) { d.Funds[0].ReportSlug = "someone-elses-slug" },
		"a different login":       func(d *backup.Document) { d.Users[0].PasswordHash = "another-hash" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := oldInstance(t)
			rewriteBackup(t, f.zipPath, edit)
			before := readFile(t, f.dbPath)

			err := f.run(backup.ReplaceHooks{}, rebuildNow)
			if !errors.Is(err, ErrOtherInstance) {
				t.Fatalf("rebuild = %v, want ErrOtherInstance", err)
			}
			assertNothingWritten(t, f, before)
		})
	}
}

func TestRebuildNeedsExactlyOneLoginInTheBackup(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*backup.Document){
		"zero logins": func(d *backup.Document) { d.Users = []backup.User{} },
		"two logins": func(d *backup.Document) {
			d.Users = append(d.Users, backup.User{ID: 2, Email: "second@rebuild.test", PasswordHash: "h", CreatedAt: 1})
		},
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := oldInstance(t)
			rewriteBackup(t, f.zipPath, edit)
			before := readFile(t, f.dbPath)

			err := f.run(backup.ReplaceHooks{}, rebuildNow)
			if !errors.Is(err, backup.ErrRebuildLogins) {
				t.Fatalf("rebuild = %v, want ErrRebuildLogins", err)
			}
			assertNothingWritten(t, f, before)
		})
	}
}

func TestRebuildRefusesABackupWhoseTotalsDisagreeWithItsRows(t *testing.T) {
	t.Parallel()
	f := oldInstance(t)
	rewriteBackup(t, f.zipPath, func(d *backup.Document) { d.Totals.Funds[0].Balance++ })
	before := readFile(t, f.dbPath)

	err := f.run(backup.ReplaceHooks{}, rebuildNow)
	if !errors.Is(err, backup.ErrTotalsMismatch) {
		t.Fatalf("rebuild = %v, want ErrTotalsMismatch", err)
	}
	assertNothingWritten(t, f, before)
}

func TestRebuildRefusesAnExistingPreRebuildName(t *testing.T) {
	t.Parallel()
	f := oldInstance(t)
	before := readFile(t, f.dbPath)
	if err := os.WriteFile(f.asidePath(), []byte("somebody's way back"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := f.run(backup.ReplaceHooks{}, rebuildNow)
	if !errors.Is(err, backup.ErrPreRebuildExists) {
		t.Fatalf("rebuild = %v, want ErrPreRebuildExists", err)
	}
	if got := string(readFile(t, f.asidePath())); got != "somebody's way back" {
		t.Errorf("the existing .pre file now holds %q, want it never overwritten", got)
	}
	// The aside file is the one thing here that is not the rebuild's.
	if err := os.Remove(f.asidePath()); err != nil {
		t.Fatal(err)
	}
	assertNothingWritten(t, f, before)
}

func TestRebuildThatFailsBeforeTheRenameLeavesTheDatabaseIntact(t *testing.T) {
	t.Parallel()
	f := oldInstance(t)
	before := readFile(t, f.dbPath)

	crash := backup.ReplaceHooks{Rename: func(_, _ string) error { return errors.New("simulated crash before the rename") }}
	err := f.run(crash, rebuildNow)
	if err == nil || !strings.Contains(err.Error(), "simulated crash") {
		t.Fatalf("rebuild = %v, want the simulated crash", err)
	}
	assertUntouched(t, f, before)
	// The receipt the rebuild had extracted is removed with the scratch.
	if _, err := os.Stat(filepath.Join(f.uploadsDir, rebuildReceiptName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the extracted receipt survived a failed rebuild (stat error %v)", err)
	}

	// The failed attempt is not in the way of the next one.
	f.out.Reset()
	if err := f.run(backup.ReplaceHooks{}, rebuildNow.Add(time.Second)); err != nil {
		t.Fatalf("rebuild after the crash = %v, want no error", err)
	}
	openRebuilt(t, f.dbPath)
}

// TestRebuildWhoseDirectorySyncFailsAfterTheRenameKeepsTheReceipts: once the
// rename has run the new file is live and references the extracted receipts, so
// a failed directory fsync is reported but rolls nothing back.
func TestRebuildWhoseDirectorySyncFailsAfterTheRenameKeepsTheReceipts(t *testing.T) {
	t.Parallel()
	f := oldInstance(t)

	hooks := backup.ReplaceHooks{SyncDir: func(string) error { return errors.New("simulated EIO") }}
	err := f.run(hooks, rebuildNow)
	if !errors.Is(err, backup.ErrSwappedUnsynced) {
		t.Fatalf("rebuild = %v, want ErrSwappedUnsynced", err)
	}
	openRebuilt(t, f.dbPath)
	if _, err := os.Stat(filepath.Join(f.uploadsDir, rebuildReceiptName)); err != nil {
		t.Errorf("the receipt the live database references was removed: %v", err)
	}
}

func TestRebuildOfADamagedDatabaseWarnsSkipsTheGateAndKeepsTheFile(t *testing.T) {
	t.Parallel()
	f := oldInstance(t)
	garbage := bytes.Repeat([]byte("this is not a database "), 200)
	if err := os.WriteFile(f.dbPath, garbage, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := f.run(backup.ReplaceHooks{}, rebuildNow); err != nil {
		t.Fatalf("rebuild over a damaged database = %v, want no error", err)
	}
	if !strings.Contains(f.out.String(), "WARNING") {
		t.Errorf("output has no warning:\n%s", f.out.String())
	}
	if kept := readFile(t, f.asidePath()); !bytes.Equal(kept, garbage) {
		t.Errorf("the damaged file was not kept aside as it was")
	}
	if snaps, _ := backup.ListPreUpgradeSnapshots(f.backupDir); len(snaps) != 0 {
		t.Errorf("a snapshot of a damaged database was taken: %v", snaps)
	}
	openRebuilt(t, f.dbPath)
}

func TestRebuildFailsClosedWhenTheLedgerCannotBeCompared(t *testing.T) {
	t.Parallel()
	f := oldInstance(t)
	// An old schema in which the ledger's table has another name.
	execRaw(t, f.dbPath,
		`DROP TRIGGER transaction_immutable_update`, `DROP TRIGGER transaction_immutable_delete`,
		`PRAGMA foreign_keys = OFF`, `ALTER TABLE "transaction" RENAME TO ledger_entry`)
	before := readFile(t, f.dbPath)

	err := f.run(backup.ReplaceHooks{}, rebuildNow)
	if !errors.Is(err, ErrCannotCompare) {
		t.Fatalf("rebuild = %v, want ErrCannotCompare", err)
	}
	assertNothingWritten(t, f, before)
}

func TestRebuildUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"--from"}, {"--bogus", "x"}, {"--from", "a.zip", "extra"}} {
		if err := rebuild(context.Background(), args, io.Discard); !errors.Is(err, errRebuildUsage) {
			t.Errorf("rebuild(%v) = %v, want the usage error", args, err)
		}
	}
}

func TestFormatRupiah(t *testing.T) {
	t.Parallel()
	for in, want := range map[int64]string{
		0: "Rp 0", 7: "Rp 7", 999: "Rp 999", 1000: "Rp 1.000", 1_150_000: "Rp 1.150.000",
		-40_000: "-Rp 40.000", 9_223_372_036_854_775_807: "Rp 9.223.372.036.854.775.807",
		-9_223_372_036_854_775_808: "-Rp 9.223.372.036.854.775.808",
	} {
		if got := formatRupiah(in); got != want {
			t.Errorf("formatRupiah(%d) = %q, want %q", in, got, want)
		}
	}
}

// TestRebuildTakesThePreviousReleasesBackup is #516's guard on ADR-012's 0.x
// rule: the backup the last tag wrote still goes through ParseUpload and
// rebuilds into the current schema with its totals proven. The fixture is the
// golden uruni.json at v0.9.0-alpha.2; refresh it from the new tag whenever
// URUNI_TAG is bumped. A schema change that makes this fail has to keep that
// backup restorable, or ship its converter in the same PR.
func TestRebuildTakesThePreviousReleasesBackup(t *testing.T) {
	t.Parallel()
	f := newRebuildFixture(t)
	raw := readFile(t, filepath.Join("testdata", "previous-release", "uruni.json"))
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	w, err := zw.Create("uruni.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.zipPath, zipBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	parsed, err := backup.ParseUpload(f.zipPath)
	if err != nil {
		t.Fatalf("ParseUpload(previous release) = %v, want no error", err)
	}
	var want int64
	for _, ft := range parsed.Document.Totals.Funds {
		want += ft.Balance
	}

	if err := f.run(backup.ReplaceHooks{}, rebuildNow); err != nil {
		t.Fatalf("rebuild from the previous release's backup = %v, want no error", err)
	}
	sqlDB := openRebuilt(t, f.dbPath)
	var got int64
	for _, fund := range parsed.Document.Funds {
		bal, err := ledger.New(sqlDB).FundBalance(context.Background(), fund.ID)
		if err != nil {
			t.Fatal(err)
		}
		got += bal.Int64()
	}
	if got != want || want == 0 {
		t.Errorf("rebuilt total = %d, backup total = %d, want them equal and non-zero", got, want)
	}
	if !strings.Contains(f.out.String(), "backup says   "+formatRupiah(want)) || !strings.Contains(f.out.String(), "rebuilt says  "+formatRupiah(want)) {
		t.Errorf("output does not show the proven totals:\n%s", f.out.String())
	}
}
