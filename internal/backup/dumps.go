// dumps.go is M6.38's (#324) own addition, on top of #323's Export/BuildZip:
// the daily server-side copy ADR-012 and ADR-013 describe. Nothing here
// writes to the database (same as export.go) - it only ever reads through
// BuildDocument/BuildZip and writes zip files under URUNI_BACKUP_DIR.
//
// (A blank line separates this from `package backup` on purpose: Go treats
// a comment with no blank line before the package clause as *the* package
// doc comment, and export.go already carries that one - revive's
// package-comments check flags a second, differently-shaped one otherwise.)

package backup

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/store"
)

// Kind is a dump's own kind (ADR-012's "date and kind"): what wrote it, not
// how it is scheduled - the two values the Cadangan card ever shows
// (copy.settings.backup.kindDaily / kindPreRestore in web/src/copy/id.ts).
type Kind string

const (
	// KindDaily is the scheduled once-a-day dump and also what
	// the boot-time format-version dump writes - it is still, in substance,
	// today's daily copy, just taken at boot instead of on the hourly tick.
	KindDaily Kind = "daily"
	// KindPreRestore is the safety-net dump ADR-012's restore path takes
	// immediately before it changes anything (#325 wires the call;
	// WritePreRestoreDump below is the function it calls).
	KindPreRestore Kind = "pre-restore"
)

// DailyRetention and PreRestoreRetention are ADR-012's two retention quotas,
// counted in *versions* per kind, never in calendar days - a day on which
// the fund did not change writes no new dump, so "last 7" can span more
// than 7 days, and that is the intended behaviour, not a bug (ADR-012:
// "retention counts versions, not days").
const (
	DailyRetention      = 7
	PreRestoreRetention = 3
)

// dumpNamePattern is the one definition of what a dump's own filename looks
// like - ListDumps, ValidDumpName (the download route's own guard against
// path traversal) and ApplyRetention (which must touch nothing this
// package did not itself write) all go through it, so there is exactly one
// place that says what counts as "this code's own file".
//
// uruni-<date>-<time>-<kind>-fv<format version>-<hash12>.zip, e.g.
// uruni-20260930-140501-daily-fv1-3f9a2c8e10b4.zip. Every field is folded
// into the name itself rather than kept in a separate index file:
//   - date+time (Asia/Jakarta, YYYYMMDD-HHMMSS): a dump written at boot for
//     the format-version check, or a same-day change after the scheduled
//     dump already ran, still needs a name of its own - retention counts
//     versions, not one slot per calendar day, so the name cannot be dated
//     to the day alone.
//   - kind: "daily" or "pre-restore", never overlapping regexp
//     alternatives, so retention can group by kind from the name alone.
//   - format version: read back without opening the zip - NeedsFormatVersionDump
//     runs once per boot and would otherwise mean unzipping every dump on
//     disk just to read one integer out of each one's uruni.json.
//   - a 12-hex-char prefix of ChangeHash's own output: lets ListDumps
//     compare "did the fund change since the last daily dump" without
//     re-reading and re-hashing an old dump's bytes off disk.
//
// The character classes below (digits, the two literal kind words, more
// digits, more hex digits) contain no path separator and no "..", so a
// full match against this pattern is already suffient on its own to rule
// out path traversal - ValidDumpName adds no allowlist beyond this regexp.
var dumpNamePattern = regexp.MustCompile(`^uruni-(\d{8})-(\d{6})-(daily|pre-restore)-fv(\d+)-([0-9a-f]{12})\.zip$`)

// DumpInfo is one dump file's identity, parsed from its own name - never
// from a database row or a sidecar index file, so a dump's metadata can
// never drift out of sync with the file it describes.
type DumpInfo struct {
	Name          string
	Kind          Kind
	FormatVersion int64
	Hash          string
	When          time.Time // Asia/Jakarta
}

// hashPrefixLen is how much of ChangeHash's full sha256 hex digest goes
// into a dump's own filename (BuildDumpName) - the one place this
// truncation happens. Every comparison against a hash parsed back out of a
// filename (WriteDailyIfNeeded's "did the fund change") must go through
// shortHash too, or it compares a 12-character prefix against a 64-character
// digest and never matches - exactly the bug
// TestWriteDailyIfNeededSkipsWhenFundUnchangedSinceLastDailyDump caught.
const hashPrefixLen = 12

// shortHash truncates ChangeHash's full digest to the length a dump's own
// filename actually carries (dumpNamePattern's final capture group), so a
// comparison against a DumpInfo.Hash parsed from a real filename is always
// between two values of the same length.
func shortHash(hash string) string {
	if len(hash) > hashPrefixLen {
		return hash[:hashPrefixLen]
	}
	return hash
}

// BuildDumpName renders a DumpInfo's own filename - see dumpNamePattern's
// comment for the shape and why each field is in it. hash is ChangeHash's
// full hex digest; only shortHash's prefix of it goes in the name (enough
// to tell two dumps apart on one instance's disk, short enough to stay
// readable in a directory listing or an `ls`).
func BuildDumpName(when time.Time, kind Kind, formatVersion int64, hash string) string {
	t := when.In(jakarta)
	return fmt.Sprintf("uruni-%s-%s-%s-fv%d-%s.zip", t.Format("20060102"), t.Format("150405"), kind, formatVersion, shortHash(hash))
}

// ParseDumpName is BuildDumpName's inverse. ok is false for anything that
// does not fully match dumpNamePattern - a foreign file an operator placed
// in the backup directory, a half-written `.uruni-backup-tmp-*` file a
// crash left behind (writeThenRename's own temp name deliberately matches
// no dump pattern), or a name from a future, differently-shaped version of
// this code.
func ParseDumpName(name string) (DumpInfo, bool) {
	m := dumpNamePattern.FindStringSubmatch(name)
	if m == nil {
		return DumpInfo{}, false
	}
	when, err := time.ParseInLocation("20060102-150405", m[1]+"-"+m[2], jakarta)
	if err != nil {
		return DumpInfo{}, false
	}
	var formatVersion int64
	if _, err := fmt.Sscanf(m[4], "%d", &formatVersion); err != nil {
		return DumpInfo{}, false
	}
	return DumpInfo{Name: name, Kind: Kind(m[3]), FormatVersion: formatVersion, Hash: m[5], When: when}, true
}

// ValidDumpName reports whether name is exactly a filename this package
// could have produced. The HTTP download route (internal/http/backup.go)
// checks this - and that the name is also one ListDumps currently returns -
// before ever joining it onto URUNI_BACKUP_DIR; dumpNamePattern's character
// classes contain no "/" and no "..", so a match here already forecloses
// path traversal on its own.
func ValidDumpName(name string) bool {
	return dumpNamePattern.MatchString(name)
}

// ListDumps reads backupDir and returns every dump this package's own
// naming pattern recognises, newest first. A file that does not match -
// including a crash's leftover temp file - is silently skipped, never
// reported as an error: this function is read by both the scheduler (on
// every tick) and the API's list route, and a stray unrelated file sitting
// in the backup directory must never break either.
func ListDumps(backupDir string) ([]DumpInfo, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil, fmt.Errorf("backup: reading %s: %w", backupDir, err)
	}

	dumps := make([]DumpInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, ok := ParseDumpName(e.Name())
		if !ok {
			continue
		}
		dumps = append(dumps, info)
	}

	sort.Slice(dumps, func(i, j int) bool {
		if !dumps[i].When.Equal(dumps[j].When) {
			return dumps[i].When.After(dumps[j].When)
		}
		return dumps[i].Name > dumps[j].Name
	})
	return dumps, nil
}

// ChangeHash is the change-only check's own definition (ADR-012: "a hash of
// uruni.json plus the receipt file list"): the exact bytes uruni.json is
// marshaled to (docBytes, from marshalDocument - the same bytes the zip holds,
// never a fresh json.Marshal of the same Document), plus which of the
// document's receipt files are actually missing from the uploads volume
// (missing, from planDump's stat; sorted here, so order never matters).
//
// Document carries no wall-clock field of its own (no "exported at"
// timestamp - see backup.go's own comment on why nothing instance- or
// export-level appears in it), so docBytes is already deterministic for an
// unchanged database: two exports of the same, untouched fund produce
// byte-identical JSON, and therefore the same hash. missing is folded in
// separately because it is the one fact about the zip's actual contents
// that docBytes does not carry - every receipt row's path is already in
// docBytes (so a receipt added, removed or re-pathed changes the hash through
// the document), but whether that file currently exists on the uploads volume
// is not, and a photo silently disappearing from disk (never from a row) is a
// real change to what the next dump would contain. Receipt file contents are
// deliberately not hashed: a stored receipt is written once under a fresh
// random name and never rewritten (ADR-011), so its name identifies its bytes.
func ChangeHash(docBytes []byte, missing []string) string {
	sorted := append([]string(nil), missing...)
	sort.Strings(sorted)

	h := sha256.New()
	h.Write(docBytes)
	for _, m := range sorted {
		h.Write([]byte{0})
		h.Write([]byte(m))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// dumpPlan is everything a dump decision needs and nothing expensive: the
// document, its exact bytes, which receipt files are gone from disk, and the
// change hash over those. Building one reads the database and stats the
// receipt files; it never reads a receipt's contents and builds no zip.
type dumpPlan struct {
	doc      Document
	docBytes []byte
	receipts []store.Receipt
	missing  []string
	hash     string
}

// planDump is the cheap half of every producer, and the only part the hourly
// scheduler runs on an unchanged fund. The document is read in one
// transaction (BuildDocument), and then, before any zip is built or any file
// touched, its totals are recomputed from its own rows (CheckTotals, #496): a
// document that disagrees with itself is refused here, so a bad dump is never
// written - there is no temp file to clean up and nothing to rename - and the
// caller's error log is the operator's signal. Restore is the worst moment to
// learn a backup was torn; this is the cheapest.
//
// The hash is ChangeHash over the document bytes and the receipts whose file
// is missing from the uploads volume, found by os.Stat. Each receipt row's
// path is already inside docBytes, so a receipt added, removed or replaced
// changes the hash through the document, exactly as before; the stat adds the
// one fact the document cannot carry (#501: it used to take reading every
// photo into a zip to learn it).
func planDump(ctx context.Context, read documentReader, uploadsDir string) (dumpPlan, error) {
	doc, receipts, err := read(ctx)
	if err != nil {
		return dumpPlan{}, err
	}
	if err := CheckTotals(doc); err != nil {
		return dumpPlan{}, fmt.Errorf("backup: refusing to write a document that disagrees with its own totals: %w", err)
	}
	docBytes, err := marshalDocument(doc)
	if err != nil {
		return dumpPlan{}, err
	}
	missing, err := missingReceipts(uploadsDir, receipts)
	if err != nil {
		return dumpPlan{}, err
	}
	return dumpPlan{doc: doc, docBytes: docBytes, receipts: receipts, missing: missing, hash: ChangeHash(docBytes, missing)}, nil
}

// missingReceipts names the receipts whose file is absent from uploadsDir,
// by stat alone - the same ErrNotExist test addReceiptToZip applies on
// read, without the read. Any other stat error is a real fault and fails,
// as the read would have.
func missingReceipts(uploadsDir string, receipts []store.Receipt) ([]string, error) {
	var missing []string
	for _, r := range receipts {
		//nolint:gosec // r.Path is a server-generated receipt filename, see addReceiptToZip
		if _, err := os.Stat(filepath.Join(uploadsDir, r.Path)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				missing = append(missing, r.Path)
				continue
			}
			return nil, fmt.Errorf("backup: checking receipt %s: %w", r.Path, err)
		}
	}
	return missing, nil
}

// dumpMaterials is a dumpPlan plus its zip: what a producer that has decided
// to write (or download) holds.
type dumpMaterials struct {
	dumpPlan
	zipBytes []byte
}

// zipFor builds the zip for a plan, reading every receipt file. missing is
// replaced with what the zip pass itself found, which is what the archive
// really lacks (the hash keeps the earlier stat; a photo vanishing between
// the two shows up as a changed hash on the next tick).
func (p dumpPlan) zipFor(assemble zipAssembler, uploadsDir string) (dumpMaterials, error) {
	zipBytes, missing, err := assemble(p.docBytes, p.receipts, uploadsDir)
	if err != nil {
		return dumpMaterials{}, err
	}
	p.missing = missing
	return dumpMaterials{dumpPlan: p, zipBytes: zipBytes}, nil
}

// buildDumpMaterials is the one path every producer takes when it has
// decided it wants the archive - the download, the boot dump, the pre-restore
// safety net and a daily dump the fund has changed for: planDump (read,
// CheckTotals, hash), then the zip.
func buildDumpMaterials(ctx context.Context, read documentReader, assemble zipAssembler, uploadsDir string) (dumpMaterials, error) {
	plan, err := planDump(ctx, read, uploadsDir)
	if err != nil {
		return dumpMaterials{}, err
	}
	return plan.zipFor(assemble, uploadsDir)
}

// tempPrefix names writeThenRename's in-flight files. RemoveStaleTemps
// matches on exactly this prefix, so it is one constant, never two strings.
const tempPrefix = ".uruni-backup-tmp-"

// RemoveStaleTemps deletes what a crash between writeThenRename's write and
// its rename leaves behind: regular files in backupDir named with this
// package's own tempPrefix, and nothing else - never a dump, never a
// foreign file. Called only at boot, before EnsureBootDump writes and
// before RunScheduler starts, so no write of this process can be in flight.
// It returns how many it removed; a file it cannot remove is an error.
func RemoveStaleTemps(backupDir string) (int, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return 0, fmt.Errorf("backup: reading %s: %w", backupDir, err)
	}
	removed := 0
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasPrefix(e.Name(), tempPrefix) {
			continue
		}
		if err := os.Remove(filepath.Join(backupDir, e.Name())); err != nil {
			return removed, fmt.Errorf("backup: removing stale temp %s: %w", e.Name(), err)
		}
		removed++
	}
	return removed, nil
}

// writeThenRename is every dump write's own shape: build the whole file in
// a temp name inside backupDir first, sync it, then one rename into its final
// name and a sync of backupDir (WriteFileAtomic, atomic.go, which also says
// why each sync is there). A crash (or a kill -9) between the steps can only
// ever leave a temp file behind - never a file under a name ListDumps,
// ValidDumpName or ApplyRetention would recognise as a real dump, since the
// temp prefix matches no dump pattern, and (with the syncs) never a dump name
// pointing at an empty file.
func writeThenRename(backupDir, name string, data []byte) error {
	if err := writeBytesAtomic(backupDir, tempPrefix+"*", name, data); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// WriteDump writes one fresh dump of kind, unconditionally - the change-only
// check is WriteDailyIfNeeded's job, not this function's. Both the
// boot-time format-version dump and #325's safety-net restore call this
// directly because both always want a fresh dump regardless of whether the
// fund changed since the last one.
func WriteDump(ctx context.Context, sqlDB *sql.DB, uploadsDir, backupDir string, kind Kind, now time.Time) (name string, err error) {
	return writeDump(ctx, readDocumentFrom(sqlDB), assembleZip, uploadsDir, backupDir, kind, now)
}

// writeDump is WriteDump over an injectable document reader (documentReader's
// own comment says why) and zip assembler.
func writeDump(ctx context.Context, read documentReader, assemble zipAssembler, uploadsDir, backupDir string, kind Kind, now time.Time) (name string, err error) {
	m, err := buildDumpMaterials(ctx, read, assemble, uploadsDir)
	if err != nil {
		return "", err
	}
	name = BuildDumpName(now, kind, m.doc.FormatVersion, m.hash)
	if err := writeThenRename(backupDir, name, m.zipBytes); err != nil {
		return "", err
	}
	return name, nil
}

// WriteDailyIfNeeded is the scheduler's own tick (scheduler.go): write a
// fresh "daily" dump exactly when both are true -
//
//  1. no daily dump exists yet for *today*, the treasurer's own calendar
//     day in Asia/Jakarta - never the kind's most recent dump's own date,
//     since a boot-time format-version dump earlier today already counts
//     as today's daily copy; and
//  2. the fund has changed since the most recent daily dump of any date
//     (ChangeHash, compared against the newest daily DumpInfo's own Hash,
//     parsed back out of its filename - never recomputed by reading an old
//     dump off disk).
//
// Both conditions, not either: a restart on a day nothing has changed since
// yesterday's dump must write nothing just because today happens to have no
// dump of its own yet (ADR-012's "retention counts versions, not days") -
// and a fund that changed twice in one day still gets only one daily dump
// for that day, not a second one chasing the second change.
func WriteDailyIfNeeded(ctx context.Context, sqlDB *sql.DB, uploadsDir, backupDir string, now time.Time) (name string, written bool, err error) {
	check := func(ctx context.Context) error { return db.QuickCheck(ctx, sqlDB) }
	return writeDailyIfNeeded(ctx, readDocumentFrom(sqlDB), assembleZip, check, uploadsDir, backupDir, now)
}

// writeDailyIfNeeded is WriteDailyIfNeeded over an injectable document reader,
// zip assembler and integrity check (nil skips the check).
func writeDailyIfNeeded(ctx context.Context, read documentReader, assemble zipAssembler, check func(context.Context) error, uploadsDir, backupDir string, now time.Time) (name string, written bool, err error) {
	dumps, err := ListDumps(backupDir)
	if err != nil {
		return "", false, err
	}

	today := now.In(jakarta).Format("2006-01-02")
	var mostRecentDailyHash string
	haveMostRecentDaily := false
	for _, d := range dumps { // newest first
		if d.Kind != KindDaily {
			continue
		}
		if !haveMostRecentDaily {
			mostRecentDailyHash = d.Hash
			haveMostRecentDaily = true
		}
		if d.When.In(jakarta).Format("2006-01-02") == today {
			return "", false, nil
		}
	}

	// Decide from the hash alone (#501): an unchanged fund assembles no zip
	// and reads no receipt file's contents, however often this is called.
	plan, err := planDump(ctx, read, uploadsDir)
	if err != nil {
		return "", false, err
	}
	if haveMostRecentDaily && shortHash(plan.hash) == mostRecentDailyHash {
		return "", false, nil
	}
	// Only a dump that will actually be written pays for the integrity check
	// (#505), so it runs about once a day, not every tick. A damaged file is
	// refused rather than dumped: a dump of it would count against the
	// 7-version retention and could push the last good backups out.
	if check != nil {
		if err := check(ctx); err != nil {
			return "", false, fmt.Errorf("backup: not writing a daily dump: %w", err)
		}
	}
	m, err := plan.zipFor(assemble, uploadsDir)
	if err != nil {
		return "", false, err
	}

	name = BuildDumpName(now, KindDaily, m.doc.FormatVersion, m.hash)
	if err := writeThenRename(backupDir, name, m.zipBytes); err != nil {
		return "", false, err
	}
	return name, true, nil
}

// WritePreRestoreDump is the safety-net dump ADR-012's restore path takes
// before it changes a single row. Restore calls it before its transaction
// opens, and `migrate down` before it rolls a step back. Unconditional, like
// WriteDump (a restore is exactly the moment "did the fund change since the
// last dump" stops being the question that matters), and it prunes its own
// kind's quota afterward so its callers do not also need to know about
// ApplyRetention.
func WritePreRestoreDump(ctx context.Context, sqlDB *sql.DB, uploadsDir, backupDir string, now time.Time) (name string, err error) {
	name, err = WriteDump(ctx, sqlDB, uploadsDir, backupDir, KindPreRestore, now)
	if err != nil {
		return "", err
	}
	if err := ApplyRetention(backupDir, DailyRetention, PreRestoreRetention); err != nil {
		return name, err
	}
	return name, nil
}

// NeedsFormatVersionDump is serve's own boot check (ADR-012: "serve writes
// a fresh dump at boot when the newest dump's format_version differs from
// its own, so a restorable backup in the current format always exists"):
// true when backupDir holds no dump at all yet (a fresh instance), or the
// single newest dump on disk - by write time, any kind - was written by a
// FormatVersion other than the one this binary is running.
func NeedsFormatVersionDump(backupDir string) (bool, error) {
	dumps, err := ListDumps(backupDir)
	if err != nil {
		return false, err
	}
	if len(dumps) == 0 {
		return true, nil
	}
	return dumps[0].FormatVersion != FormatVersion, nil
}

// ApplyRetention deletes every dump beyond each kind's own quota, newest
// kept: dailyKeep for KindDaily, preRestoreKeep for KindPreRestore, in
// separate quotas exactly as ADR-012 specifies. The only files it can ever
// remove are ones ListDumps itself just matched against dumpNamePattern -
// this function never globs, never touches a name it did not get back from
// ListDumps, so a foreign file placed in backupDir by the operator (or by a
// future feature that is not this package) is never at risk from it.
func ApplyRetention(backupDir string, dailyKeep, preRestoreKeep int) error {
	dumps, err := ListDumps(backupDir) // newest first
	if err != nil {
		return err
	}

	quota := map[Kind]int{KindDaily: dailyKeep, KindPreRestore: preRestoreKeep}
	seen := map[Kind]int{}

	for _, d := range dumps {
		seen[d.Kind]++
		if seen[d.Kind] <= quota[d.Kind] {
			continue
		}
		if err := os.Remove(filepath.Join(backupDir, d.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backup: removing old dump %s: %w", d.Name, err)
		}
	}
	return nil
}

// EnsureBootDump is `serve`'s one boot-time call (cmd/uruni/main.go): write
// and retain a fresh dump when NeedsFormatVersionDump says the newest one
// on disk (or the absence of any) is not in the running binary's format.
// It returns an error rather than swallowing one itself, but main.go's own
// call site treats that error as non-fatal - logged, not a refusal to
// boot: a backup write failing must never keep the treasurer from
// recording a transaction, which is the app's actual job.
func EnsureBootDump(ctx context.Context, sqlDB *sql.DB, uploadsDir, backupDir string, now time.Time, logger *slog.Logger) error {
	// Also sweeps any restore-staging file a restart between an inspect and
	// its confirm left behind (issue #344): CreateRestoreStagingTemp names
	// every one of those files with this same tempPrefix, so there is
	// nothing separate for boot to sweep - a leftover stage is, to this
	// function, just another interrupted write.
	if removed, err := RemoveStaleTemps(backupDir); err != nil {
		logger.Warn("backup: could not clear stale temp files", "error", err)
	} else if removed > 0 {
		logger.Info("backup: removed temp files left by an interrupted dump", "count", removed)
	}

	needed, err := NeedsFormatVersionDump(backupDir)
	if err != nil {
		return err
	}
	if !needed {
		return nil
	}
	name, err := WriteDump(ctx, sqlDB, uploadsDir, backupDir, KindDaily, now)
	if err != nil {
		return err
	}
	// One message for both triggers NeedsFormatVersionDump covers - no dump
	// at all yet (a fresh instance) or none in the running format - so an
	// operator reading the log is never told a version changed when none did.
	logger.Info("wrote boot backup dump (none yet in the current format)", "name", name, "format_version", FormatVersion)
	return ApplyRetention(backupDir, DailyRetention, PreRestoreRetention)
}
