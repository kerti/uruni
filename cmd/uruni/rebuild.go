package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/backup"
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/lock"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// ErrSchemaCurrent is rebuild's refusal when the database already has this
// binary's schema: restoring in the app is the tool for that case, and it
// keeps the live login and the safety-net dump.
var ErrSchemaCurrent = errors.New("the database already has this version's schema")

// errRebuildUsage is returned for a missing or unknown argument.
var errRebuildUsage = errors.New("usage: uruni rebuild --from <backup.zip>")

// rebuildRun is everything one rebuild needs, so a test can drive it with a
// fixed clock and version without touching the process environment.
type rebuildRun struct {
	cfg     config.Config
	zipPath string
	version string
	now     time.Time
	out     io.Writer
	logger  *slog.Logger
	hooks   backup.ReplaceHooks
}

// rebuild is ADR-012's schema-changing upgrade in one command (#516): build a
// new database file of this binary's schema from a backup the old binary
// wrote, and swap it in, keeping the old file aside. The operator runs it with
// `serve` stopped. 0.x only: the v1.0.0 upgrade chain (ADR-012) replaces it.
//
// There is no --from-live: this binary's queries cannot read the old schema,
// so the way across is the backup, a format that is stable across schemas.
func rebuild(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rebuild", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	from := fs.String("from", "", "")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w (%v)", errRebuildUsage, err) //nolint:errorlint
	}
	if *from == "" || fs.NArg() > 0 {
		return errRebuildUsage
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return runRebuild(ctx, rebuildRun{
		cfg:     cfg,
		zipPath: *from,
		version: version,
		now:     time.Now(),
		out:     out,
		logger:  newLogger(cfg, os.Stderr),
	})
}

// printer writes operator output and remembers the first write error, so the
// flow below reads as prose. It prints to the writer it is given (stdout):
// the totals are amounts, which ADR-022 keeps out of logs.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) f(format string, a ...any) {
	if p.err == nil {
		_, p.err = fmt.Fprintf(p.w, format+"\n", a...)
	}
}

// runRebuild is the whole command. The order is the safety argument:
//
//  1. Read-only checks, nothing written: instance lock, the backup parses,
//     the database is not already on this schema, the file's owner, the name
//     the old file will be kept under is free, and the staleness / identity
//     gate against the old file.
//  2. Then the writes: the #517 snapshot and its marker (never MarkBooted -
//     only a serve boot has booted), then a scratch file in the database's own
//     directory, built from the embedded schema and the backup, proven, made
//     self-contained and checked again.
//  3. Then the swap: the old file kept aside, one rename, a directory fsync.
//
// Scratch files are removed on every failure path, so a refused or failed
// rebuild leaves uruni.db exactly as it found it.
func runRebuild(ctx context.Context, r rebuildRun) (err error) {
	cfg := r.cfg
	dbDir := filepath.Dir(cfg.DBPath)
	pr := &printer{w: r.out}

	instanceLock, err := lock.Acquire(lock.PathFor(cfg.DBPath))
	if errors.Is(err, lock.ErrLocked) {
		return fmt.Errorf("%w: stop it before running rebuild", ErrServerRunning)
	}
	if err != nil {
		return err
	}
	defer func() { _ = instanceLock.Release() }()

	parsed, err := backup.ParseUpload(r.zipPath)
	if err != nil {
		if errors.Is(err, backup.ErrFormatVersionOlder) {
			return fmt.Errorf("reading the backup %s: %w - it was written by a version whose backup format this one no longer reads; "+
				"see the release notes (ADR-012: through 0.x a schema change keeps the previous backup restorable or ships its converter)", r.zipPath, err)
		}
		return fmt.Errorf("reading the backup %s: %w", r.zipPath, err)
	}
	doc := parsed.Document
	if err := backup.CheckRebuildLogin(doc); err != nil {
		return fmt.Errorf("the backup %s cannot be rebuilt from: %w", r.zipPath, err)
	}
	backupSum, err := backupTotal(doc)
	if err != nil {
		return err
	}

	pr.f("rebuilding %s from %s", cfg.DBPath, r.zipPath)
	pr.f("backup taken %s; funds: %s", backupDate(doc), fundNames(doc))

	asidePath := filepath.Join(dbDir, backup.PreRebuildName(cfg.DBPath, r.version, r.now))
	old, err := inspectOldDatabase(ctx, cfg.DBPath, asidePath)
	if err != nil {
		return err
	}
	defer func() { _ = old.close() }()

	oldSum, oldSumOK := int64(0), false
	switch {
	case !old.exists:
		pr.f("there is no database at %s yet: nothing to compare the backup with and nothing to keep", cfg.DBPath)
	case old.damaged != "":
		pr.f("WARNING: the existing database could not be checked (%s).", old.damaged)
		pr.f("WARNING: it is not compared with the backup and no snapshot is taken of it. Its file is still kept aside as it is.")
	case old.empty:
		pr.f("the existing database has no tables: nothing to compare the backup with")
	default:
		report, gateErr := checkBackupMatchesDatabase(ctx, old.sqlDB, doc)
		pr.f("checked against the existing database (nothing has been changed yet):")
		pr.f("  compared:     %s", strings.Join(report.compared, ", "))
		pr.f("  not compared: %s", strings.Join(report.notCompared, ", "))
		if gateErr != nil {
			return gateErr
		}
		oldSum, oldSumOK = rawLedgerSum(ctx, old.sqlDB)
	}
	if pr.err != nil {
		return pr.err
	}

	// From here on the command writes. The uploads directory takes receipt
	// files the way serve's does, so it gets serve's check.
	if err := config.EnsureUploadsDirWritable(cfg.UploadsDir); err != nil {
		return err
	}
	if _, err := backup.RemoveRebuildScratch(dbDir); err != nil {
		return err
	}

	if old.healthy() {
		// The checks ran on a read-only handle. The snapshot takes a read-write one,
		// whose close below also folds the WAL into the main file.
		if err := old.close(); err != nil {
			return fmt.Errorf("closing the old database: %w", err)
		}
		rw, err := db.Open(ctx, cfg.DBPath)
		if err != nil {
			return fmt.Errorf("opening the old database for its snapshot: %w", err)
		}
		old.sqlDB = rw
		// The #517 guard, advisory on a damaged file only (see above). Its marker
		// says a snapshot was taken for this version, so the serve that follows the
		// rebuild snapshots nothing again; it does not say the version booted.
		if err := config.EnsureBackupDirWritable(cfg.BackupDir); err != nil {
			return err
		}
		if err := backup.EnsurePreUpgradeSnapshot(ctx, old.sqlDB, cfg.BackupDir, r.version, r.now, r.logger); err != nil {
			return fmt.Errorf("not rebuilding without a snapshot of the old database to go back to in %s (free space there): %w", cfg.BackupDir, err)
		}
	}
	// The old handle must be closed before the file is copied or replaced: closing
	// the last connection folds the WAL into the main file.
	if err := old.close(); err != nil && old.healthy() {
		return fmt.Errorf("closing the old database: %w", err)
	}

	swapped := false
	var created []string
	defer func() {
		if swapped {
			return
		}
		_, _ = backup.RemoveRebuildScratch(dbDir)
		backup.RemoveCreatedReceipts(created)
	}()

	readyPath := filepath.Join(dbDir, backup.RebuildScratchPrefix+"ready.db")
	var rebuiltSum int64
	created, rebuiltSum, err = buildScratch(ctx, r, parsed, readyPath)
	if err != nil {
		return err
	}
	if rebuiltSum != backupSum {
		return fmt.Errorf("%w: the backup says %s, the rebuilt database says %s", backup.ErrTotalsMismatch, formatRupiah(backupSum), formatRupiah(rebuiltSum))
	}

	if err := backup.ReplaceDatabase(cfg.DBPath, readyPath, asidePath, r.hooks); err != nil {
		// Renamed but not synced: the new file is live and references the receipts
		// just written, so nothing may be cleaned up. The operator re-checks the
		// disk; the data is already in place.
		swapped = errors.Is(err, backup.ErrSwappedUnsynced)
		return err
	}
	swapped = true

	pr.f("")
	pr.f("totals (all funds, rupiah):")
	pr.f("  backup says   %s", formatRupiah(backupSum))
	pr.f("  rebuilt says  %s", formatRupiah(rebuiltSum))
	switch {
	case oldSumOK:
		pr.f("  old database  %s (raw sum of its ledger)", formatRupiah(oldSum))
	case !old.exists:
		pr.f("  old database  n/a (there was none)")
	default:
		pr.f("  old database  n/a (its ledger could not be read)")
	}
	if old.exists {
		pr.f("the old database is kept as %s", asidePath)
	}
	pr.f("login carried over: %s (same password as in the backup)", doc.Users[0].Email)
	pr.f("done. Start the app on this version.")
	return pr.err
}

// oldDatabase is what the checks learned about the file at the database path.
type oldDatabase struct {
	exists bool
	sqlDB  *sql.DB
	// damaged is why the file could not be read and checked ("" if it could). A
	// damaged file is not compared and not snapshotted (the snapshot would copy
	// the damage), but it is still kept aside byte for byte.
	damaged string
	// empty means the file opened and has no tables (an interrupted first run).
	empty bool
}

func (o *oldDatabase) healthy() bool { return o.exists && o.damaged == "" && !o.empty }

func (o *oldDatabase) close() error {
	if o.sqlDB == nil {
		return nil
	}
	sqlDB := o.sqlDB
	o.sqlDB = nil
	return sqlDB.Close()
}

// inspectOldDatabase runs the read-only checks on the existing file: it must be
// a regular file owned by the running user, its pre-rebuild name must be free,
// it must open and pass quick_check, and its schema must not already be this
// binary's. An absent file is not an error.
func inspectOldDatabase(ctx context.Context, dbPath, asidePath string) (*oldDatabase, error) {
	old := &oldDatabase{}
	info, err := os.Lstat(dbPath)
	if errors.Is(err, os.ErrNotExist) {
		return old, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dbPath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", dbPath)
	}
	old.exists = true
	if err := backup.CheckDatabaseOwner(dbPath); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(asidePath); err == nil {
		return nil, fmt.Errorf("%w: %s - wait a second or move it away", backup.ErrPreRebuildExists, asidePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", asidePath, err)
	}

	// Read-only, so nothing about the file changes before the checks pass. Only
	// damage SQLite itself reports (db.IsDamage) skips the gate; a busy, locked
	// or out-of-space error refuses, since the file behind it may be healthy and
	// the gate is what stops a stale or another instance's backup.
	notChecked := func(what string, err error) (*oldDatabase, error) {
		if db.IsDamage(err) {
			old.damaged = what + ": " + err.Error()
			return old, nil
		}
		_ = old.close()
		return nil, fmt.Errorf("%s %s (not a sign of damage, so rebuild will not skip its checks): %w", dbPath, what, err)
	}
	sqlDB, err := db.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return notChecked("could not be opened", err)
	}
	old.sqlDB = sqlDB
	if err := db.QuickCheck(ctx, sqlDB); err != nil {
		return notChecked("failed its integrity check", err)
	}
	tables, err := tableNames(ctx, sqlDB)
	if err != nil {
		return notChecked("could not have its tables listed", err)
	}
	if len(tables) == 0 {
		old.empty = true
		return old, nil
	}
	diff, err := db.CheckSchema(ctx, sqlDB)
	if err != nil {
		return notChecked("could not have its schema read", err)
	}
	if diff.Matches() {
		_ = old.close()
		return nil, fmt.Errorf("%w: %s - restore the backup in the app (Pengaturan > Cadangan), which keeps the login and writes a safety copy; rebuild is for a changed schema", ErrSchemaCurrent, dbPath)
	}
	return old, nil
}

// buildScratch builds the new database at readyPath and returns the receipt
// files it created and the total it rebuilt. The file is made in two steps so
// what gets swapped in is self-contained: a build database (WAL, like any
// other) is migrated and filled, proven, then written out with VACUUM INTO
// through the atomic write helper (fsynced, no -wal or -shm), and that finished
// file is checked again, read-only, before it is trusted.
func buildScratch(ctx context.Context, r rebuildRun, parsed backup.ParsedUpload, readyPath string) (created []string, rebuiltSum int64, err error) {
	dbDir := filepath.Dir(readyPath)
	buildPath := filepath.Join(dbDir, backup.RebuildScratchPrefix+"build.db")

	sqlDB, err := db.Open(ctx, buildPath)
	if err != nil {
		return nil, 0, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = sqlDB.Close()
		}
	}()
	// The receipt files RestoreForRebuild created are returned even when a later
	// step fails, so the caller removes them with the scratch files.

	if _, err = db.Up(ctx, sqlDB, r.logger); err != nil {
		return nil, 0, fmt.Errorf("migrating the new database: %w", err)
	}
	if created, err = backup.RestoreForRebuild(ctx, sqlDB, r.cfg.UploadsDir, parsed); err != nil {
		return nil, 0, err
	}
	if err = verifyFinished(ctx, sqlDB, "the rebuilt database"); err != nil {
		return created, 0, err
	}

	err = backup.WriteFileAtomic(dbDir, backup.RebuildScratchPrefix+"final-*", filepath.Base(readyPath), func(w io.Writer) error {
		f, ok := w.(*os.File)
		if !ok {
			return fmt.Errorf("no temp file to write into (%T)", w)
		}
		_, err := sqlDB.ExecContext(ctx, "VACUUM INTO ?", f.Name())
		return err
	})
	if err != nil {
		return created, 0, fmt.Errorf("writing the finished database: %w", err)
	}
	closed = true
	if err = sqlDB.Close(); err != nil {
		return created, 0, fmt.Errorf("closing the build database: %w", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err = os.Remove(buildPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return created, 0, fmt.Errorf("removing the build database: %w", err)
		}
	}

	ro, err := db.OpenReadOnly(ctx, readyPath)
	if err != nil {
		return created, 0, err
	}
	defer func() { _ = ro.Close() }()
	if err = verifyFinished(ctx, ro, "the finished database"); err != nil {
		return created, 0, err
	}
	rebuiltSum, err = rebuiltTotal(ctx, ro, parsed.Document)
	if err != nil {
		return created, 0, err
	}
	return created, rebuiltSum, nil
}

// verifyFinished is the check a database file must pass before it is trusted:
// quick_check and the schema this binary creates.
func verifyFinished(ctx context.Context, sqlDB *sql.DB, what string) error {
	if err := db.QuickCheck(ctx, sqlDB); err != nil {
		return fmt.Errorf("%s failed its integrity check: %w", what, err)
	}
	diff, err := db.CheckSchema(ctx, sqlDB)
	if err != nil {
		return err
	}
	if !diff.Matches() {
		return fmt.Errorf("%w: %s differs (%s)", ErrSchemaMismatch, what, diff.Summary(schemaDiffNamesShown))
	}
	return nil
}

// rebuiltTotal sums the fund balances of the finished file, read with the same
// generated balance queries the app uses, and checks it holds exactly one login.
func rebuiltTotal(ctx context.Context, sqlDB *sql.DB, doc backup.Document) (int64, error) {
	q := store.New(sqlDB)
	users, err := q.CountUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("counting the rebuilt login: %w", err)
	}
	if users != 1 {
		return 0, fmt.Errorf("%w: the rebuilt database holds %d logins", backup.ErrRebuildLogins, users)
	}
	var total money.Amount
	for _, f := range doc.Funds {
		v, err := q.FundBalance(ctx, f.ID)
		if err != nil {
			return 0, fmt.Errorf("recomputing fund %d from the finished database: %w", f.ID, err)
		}
		if total, err = total.Add(money.FromDB(v)); err != nil {
			return 0, fmt.Errorf("summing the rebuilt fund balances: %w", err)
		}
	}
	return total.Int64(), nil
}

func backupDate(doc backup.Document) string {
	if d := backup.NewestDate(doc); d != "" {
		return d
	}
	return "(no date)"
}

func fundNames(doc backup.Document) string {
	if len(doc.Funds) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(doc.Funds))
	for _, f := range doc.Funds {
		names = append(names, f.Name)
	}
	return strings.Join(names, ", ")
}
