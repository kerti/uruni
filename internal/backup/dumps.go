// dumps.go adds the daily server-side copy ADR-012 and ADR-013 describe, on
// top of Export/BuildZip. Nothing here writes to the database (as in
// export.go): it reads through BuildDocument/BuildZip and writes zip files
// under URUNI_BACKUP_DIR.
//
// (A blank line separates this from `package backup` so revive does not read
// it as a second package doc comment; export.go carries the real one.)

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
	"github.com/kerti/uruni/internal/tz"
)

// Kind is a dump's kind (ADR-012's "date and kind"): what wrote it, not how it
// is scheduled. These are the two values the Cadangan card shows
// (copy.settings.backup.kindDaily / kindPreRestore in web/src/copy/id.ts).
type Kind string

const (
	// KindDaily is the scheduled once-a-day dump, and also what the boot-time
	// format-version dump writes: in substance, today's daily copy.
	KindDaily Kind = "daily"
	// KindPreRestore is the safety-net dump ADR-012's restore path takes
	// immediately before it changes anything (WritePreRestoreDump).
	KindPreRestore Kind = "pre-restore"
)

// DailyRetention and PreRestoreRetention are ADR-012's two retention quotas,
// counted in versions per kind, never calendar days: a day on which the fund
// did not change writes no dump, so "last 7" can span more than 7 days
// (ADR-012: "retention counts versions, not days").
const (
	DailyRetention      = 7
	PreRestoreRetention = 3
)

// dumpNamePattern is the one definition of a dump's filename. ListDumps,
// ValidDumpName (the download route's path-traversal guard) and
// ApplyRetention (which must touch nothing this package did not write) all
// go through it.
//
// uruni-<date>-<time>[-<zone token>]-<kind>-fv<format version>-<hash12>.zip,
// e.g. uruni-20260930-140501-daily-fv2-3f9a2c8e10b4.zip. Every field is in the
// name rather than a separate index file:
//   - date+time (YYYYMMDD-HHMMSS, in the fund's zone): retention counts
//     versions, so a boot dump or a same-day change after the scheduled dump
//     needs a name of its own; the name cannot be dated to the day alone.
//   - zone token (ADR-041): "utc8" (Asia/Makassar, WITA) or "utc9"
//     (Asia/Jayapura, WIT), written only outside WIB. A wall-clock time without
//     its zone is ambiguous the moment the zone can change or the file leaves
//     the host. A name with no token is Asia/Jakarta, whenever it was written,
//     so every existing dump keeps its meaning and nothing a WIB instance
//     writes changes shape. The parse reads the stamp in its own zone, so the
//     instant stays exact whatever the fund's zone is now.
//   - kind: "daily" or "pre-restore", so retention can group by kind from
//     the name alone.
//   - format version: readable without opening the zip, so
//     NeedsFormatVersionDump does not unzip every dump once per boot.
//   - a 12-hex-char prefix of ChangeHash: lets ListDumps compare "did the
//     fund change since the last daily dump" without re-hashing old dumps.
//
// The character classes contain no path separator and no "..", so a full
// match already rules out path traversal; ValidDumpName adds no allowlist.
var dumpNamePattern = regexp.MustCompile(`^uruni-(\d{8})-(\d{6})(?:-(utc8|utc9))?-(daily|pre-restore)-fv(\d+)-([0-9a-f]{12})\.zip$`)

// DumpInfo is one dump file's identity, parsed from its name, never from a
// database row or sidecar index, so the metadata cannot drift from the file.
type DumpInfo struct {
	Name          string
	Kind          Kind
	FormatVersion int64
	Hash          string
	Zone          string    // the zone the name is stamped in: one of tz.Names()
	When          time.Time // in Zone
}

// zoneTokens maps a zone to the token its dump names carry. WIB has none: a
// name without a token is Asia/Jakarta (ADR-041).
var zoneTokens = map[string]string{tz.Jakarta: "", tz.Makassar: "utc8", tz.Jayapura: "utc9"}

// zoneForToken is zoneTokens read backwards; "" (no token) is WIB.
func zoneForToken(token string) string {
	for zone, t := range zoneTokens {
		if t == token {
			return zone
		}
	}
	return ""
}

// InstanceZone is the zone an instance-wide job reckons its day in: the first
// fund's, by the rule resolveFund applies (ADR-030, ADR-041), or tz.Default when
// no fund exists yet (the boot dump before setup). A zone tz.Load refuses is an
// error, not a fallback.
func InstanceZone(ctx context.Context, q store.Querier) (string, error) {
	funds, err := q.ListFunds(ctx)
	if err != nil {
		return "", fmt.Errorf("backup: reading the fund's time zone: %w", err)
	}
	if len(funds) == 0 {
		return tz.Default, nil
	}
	if _, err := tz.Load(funds[0].TimeZone); err != nil {
		return "", fmt.Errorf("backup: fund %d: %w", funds[0].ID, err)
	}
	return funds[0].TimeZone, nil
}

// hashPrefixLen is how much of ChangeHash's sha256 hex digest goes into a
// dump's filename (BuildDumpName), the one place the truncation happens.
// Every comparison against a hash parsed from a filename must go through
// shortHash, or a 12-character prefix is compared with a 64-character digest
// and never matches.
const hashPrefixLen = 12

// shortHash truncates ChangeHash's digest to the length a filename carries
// (dumpNamePattern's last group), so comparisons are between equal-length
// values.
func shortHash(hash string) string {
	if len(hash) > hashPrefixLen {
		return hash[:hashPrefixLen]
	}
	return hash
}

// BuildDumpName renders a DumpInfo's filename; see dumpNamePattern. when is
// stamped in zone, one of tz.Names() (a name outside them is a programming
// error, since every caller takes it from a fund row or tz.Default, and panics).
// hash is ChangeHash's full hex digest; only shortHash's prefix goes in the name.
func BuildDumpName(when time.Time, zone string, kind Kind, formatVersion int64, hash string) string {
	loc, err := tz.Load(zone)
	if err != nil {
		panic("backup: BuildDumpName: " + err.Error())
	}
	t := when.In(loc)
	token := zoneTokens[zone]
	if token != "" {
		token = "-" + token
	}
	return fmt.Sprintf("uruni-%s-%s%s-%s-fv%d-%s.zip", t.Format("20060102"), t.Format("150405"), token, kind, formatVersion, shortHash(hash))
}

// ParseDumpName is BuildDumpName's inverse. ok is false for anything that
// does not fully match dumpNamePattern: a foreign file in the backup
// directory, a half-written temp file a crash left behind, or a name from a
// differently-shaped future version.
func ParseDumpName(name string) (DumpInfo, bool) {
	m := dumpNamePattern.FindStringSubmatch(name)
	if m == nil {
		return DumpInfo{}, false
	}
	zone := zoneForToken(m[3])
	loc, err := tz.Load(zone)
	if err != nil {
		return DumpInfo{}, false
	}
	when, err := time.ParseInLocation("20060102-150405", m[1]+"-"+m[2], loc)
	if err != nil {
		return DumpInfo{}, false
	}
	var formatVersion int64
	if _, err := fmt.Sscanf(m[5], "%d", &formatVersion); err != nil {
		return DumpInfo{}, false
	}
	return DumpInfo{Name: name, Kind: Kind(m[4]), FormatVersion: formatVersion, Hash: m[6], Zone: zone, When: when}, true
}

// ValidDumpName reports whether name is exactly a filename this package could
// have produced. The download route (internal/http/backup.go) checks this,
// and that ListDumps returns it, before joining it onto URUNI_BACKUP_DIR;
// the pattern has no "/" or "..", so a match forecloses path traversal.
func ValidDumpName(name string) bool {
	return dumpNamePattern.MatchString(name)
}

// ListDumps reads backupDir and returns every dump matching the naming
// pattern, newest first. A non-matching file (including a crash's temp file)
// is skipped, never an error: the scheduler (every tick) and the list route
// both read this, and a stray file must not break either.
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

// ChangeHash is the change-only check (ADR-012: "a hash of uruni.json plus
// the receipt file list"): the exact bytes uruni.json is marshaled to
// (docBytes, from marshalDocument, the same bytes the zip holds), plus which
// of the document's receipt files are missing from the uploads volume
// (missing, from planDump's stat; sorted here so order never matters).
//
// Document carries no wall-clock field (see backup.go), so docBytes is
// deterministic for an unchanged database: two exports of an untouched fund
// hash the same. missing is folded in because it is the one fact about the
// zip's contents docBytes lacks: a receipt added, removed or re-pathed
// changes docBytes, but a photo vanishing from disk (never from a row) is
// still a real change to what the next dump holds. Receipt contents are not
// hashed: a stored receipt is written once under a fresh random name and
// never rewritten (ADR-011), so its name identifies its bytes.
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
// transaction (BuildDocument), then, before any zip is built or file
// touched, its totals are recomputed from its rows (CheckTotals, #496): a
// self-contradicting document is refused, so a bad dump is never written and
// the caller's error log is the operator's signal. Restore is the worst
// moment to learn a backup was torn; this is the cheapest.
//
// The hash is ChangeHash over the document bytes and the receipts whose file
// is missing from the uploads volume, found by os.Stat. Receipt paths are
// inside docBytes, so a receipt added, removed or replaced changes the hash
// through the document; the stat adds the one fact the document cannot carry.
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

// missingReceipts names the receipts whose file is absent from uploadsDir, by
// stat alone (the ErrNotExist test addReceiptToZip applies, without the
// read). Any other stat error is a real fault and fails.
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
// replaced with what the zip pass found, which is what the archive really
// lacks (a photo vanishing between the stat and now shows as a changed hash
// on the next tick).
func (p dumpPlan) zipFor(assemble zipAssembler, uploadsDir string) (dumpMaterials, error) {
	zipBytes, missing, err := assemble(p.docBytes, p.receipts, uploadsDir)
	if err != nil {
		return dumpMaterials{}, err
	}
	p.missing = missing
	return dumpMaterials{dumpPlan: p, zipBytes: zipBytes}, nil
}

// buildDumpMaterials is the one path every producer takes once it wants the
// archive (download, boot dump, pre-restore dump, a changed daily dump):
// planDump (read, CheckTotals, hash), then the zip.
func buildDumpMaterials(ctx context.Context, read documentReader, assemble zipAssembler, uploadsDir string) (dumpMaterials, error) {
	plan, err := planDump(ctx, read, uploadsDir)
	if err != nil {
		return dumpMaterials{}, err
	}
	return plan.zipFor(assemble, uploadsDir)
}

// tempPrefix names writeThenRename's in-flight files. RemoveStaleTemps
// matches exactly this prefix.
const tempPrefix = ".uruni-backup-tmp-"

// RemoveStaleTemps deletes what a crash between writeThenRename's write and
// rename leaves behind: regular files in backupDir with tempPrefix, nothing
// else. Called only at boot, before EnsureBootDump and RunScheduler, so no
// write of this process can be in flight. It returns how many it removed; a
// file it cannot remove is an error.
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

// writeThenRename is every dump write's shape: build the whole file under a
// temp name in backupDir, sync it, rename it into its final name, and sync
// backupDir (WriteFileAtomic, atomic.go, says why each sync is there). A
// crash can only leave a temp file behind, never a file under a dump name,
// and with the syncs never a dump name pointing at an empty file.
func writeThenRename(backupDir, name string, data []byte) error {
	if err := writeBytesAtomic(backupDir, tempPrefix+"*", name, data); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// WriteDump writes one fresh dump of kind unconditionally; the change-only
// check is WriteDailyIfNeeded's job. The boot-time format-version dump and
// the restore safety net call this because both always want a fresh dump.
//
// The name is stamped in the instance's zone (InstanceZone): the first fund's,
// or tz.Default before setup.
func WriteDump(ctx context.Context, sqlDB *sql.DB, uploadsDir, backupDir string, kind Kind, now time.Time) (name string, err error) {
	zone, err := InstanceZone(ctx, store.New(sqlDB))
	if err != nil {
		return "", err
	}
	return writeDump(ctx, readDocumentFrom(sqlDB), assembleZip, uploadsDir, backupDir, zone, kind, now)
}

// writeDump is WriteDump over an injectable document reader and zip assembler,
// with the zone the name is stamped in already resolved.
func writeDump(ctx context.Context, read documentReader, assemble zipAssembler, uploadsDir, backupDir, zone string, kind Kind, now time.Time) (name string, err error) {
	if _, err := tz.Load(zone); err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	m, err := buildDumpMaterials(ctx, read, assemble, uploadsDir)
	if err != nil {
		return "", err
	}
	name = BuildDumpName(now, zone, kind, m.doc.FormatVersion, m.hash)
	if err := writeThenRename(backupDir, name, m.zipBytes); err != nil {
		return "", err
	}
	return name, nil
}

// WriteDailyIfNeeded is the scheduler's tick (scheduler.go): write a fresh
// "daily" dump exactly when both are true -
//
//  1. no daily dump exists yet for today in the fund's zone (not the most
//     recent dump's date: a boot-time dump earlier today already counts as
//     today's daily copy; and the comparison is of instants, both read in the
//     zone the fund has now, so a zone change never makes yesterday evening's
//     dump count as today's); and
//  2. the fund has changed since the most recent daily dump of any date
//     (ChangeHash against that DumpInfo's Hash, parsed from its filename,
//     never recomputed from the old dump).
//
// Both, not either: a restart on a day nothing changed since yesterday's dump
// writes nothing (ADR-012: retention counts versions, not days), and a fund
// that changed twice in a day still gets one daily dump.
func WriteDailyIfNeeded(ctx context.Context, sqlDB *sql.DB, uploadsDir, backupDir string, now time.Time) (name string, written bool, err error) {
	zone, err := InstanceZone(ctx, store.New(sqlDB))
	if err != nil {
		return "", false, err
	}
	check := func(ctx context.Context) error { return db.QuickCheck(ctx, sqlDB) }
	return writeDailyIfNeeded(ctx, readDocumentFrom(sqlDB), assembleZip, check, uploadsDir, backupDir, zone, now)
}

// writeDailyIfNeeded is WriteDailyIfNeeded over an injectable document reader,
// zip assembler and integrity check (nil skips the check), with the fund's zone
// already resolved.
func writeDailyIfNeeded(ctx context.Context, read documentReader, assemble zipAssembler, check func(context.Context) error, uploadsDir, backupDir, zone string, now time.Time) (name string, written bool, err error) {
	loc, err := tz.Load(zone)
	if err != nil {
		return "", false, fmt.Errorf("backup: %w", err)
	}
	dumps, err := ListDumps(backupDir)
	if err != nil {
		return "", false, err
	}

	today := now.In(loc).Format("2006-01-02")
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
		if d.When.In(loc).Format("2006-01-02") == today {
			return "", false, nil
		}
	}

	// Decide from the hash alone (#501): an unchanged fund assembles no zip and
	// reads no receipt contents, however often this is called.
	plan, err := planDump(ctx, read, uploadsDir)
	if err != nil {
		return "", false, err
	}
	if haveMostRecentDaily && shortHash(plan.hash) == mostRecentDailyHash {
		return "", false, nil
	}
	// Only a dump that will be written pays for the integrity check (#505), so it
	// runs about once a day. A damaged file is refused: dumping it would count
	// against the 7-version retention and could push the last good backups out.
	if check != nil {
		if err := check(ctx); err != nil {
			return "", false, fmt.Errorf("backup: not writing a daily dump: %w", err)
		}
	}
	m, err := plan.zipFor(assemble, uploadsDir)
	if err != nil {
		return "", false, err
	}

	name = BuildDumpName(now, zone, KindDaily, m.doc.FormatVersion, m.hash)
	if err := writeThenRename(backupDir, name, m.zipBytes); err != nil {
		return "", false, err
	}
	return name, true, nil
}

// WritePreRestoreDump is the safety-net dump ADR-012's restore path takes
// before it changes a row. Restore calls it before its transaction opens,
// and `migrate down` before it rolls a step back. Unconditional, like
// WriteDump, and it prunes its kind's quota afterward so callers need not
// know about ApplyRetention.
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

// NeedsFormatVersionDump is serve's boot check (ADR-012: serve writes a fresh
// dump at boot when the newest dump's format_version differs from its own, so
// a restorable backup in the current format always exists): true when
// backupDir holds no dump, or the newest dump on disk (by write time, any
// kind) was written by a different FormatVersion.
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

// ApplyRetention deletes every dump beyond each kind's quota, newest kept:
// dailyKeep for KindDaily, preRestoreKeep for KindPreRestore (ADR-012). It
// only removes names ListDumps matched against dumpNamePattern, never globs,
// so a foreign file in backupDir is safe.
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

// EnsureBootDump is serve's boot-time call (cmd/uruni/main.go): write and
// retain a fresh dump when NeedsFormatVersionDump says the newest one (or
// none) is not in the running binary's format. It returns an error; main.go
// treats it as non-fatal, since a backup failing must never keep the
// treasurer from recording a transaction.
func EnsureBootDump(ctx context.Context, sqlDB *sql.DB, uploadsDir, backupDir string, now time.Time, logger *slog.Logger) error {
	// Also sweeps any restore-staging file a restart between an inspect and its
	// confirm left behind: CreateRestoreStagingTemp uses tempPrefix, so a
	// leftover stage is just another interrupted write.
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
	// One message for both triggers NeedsFormatVersionDump covers, so the log
	// never says a version changed when none did.
	logger.Info("wrote boot backup dump (none yet in the current format)", "name", name, "format_version", FormatVersion)
	return ApplyRetention(backupDir, DailyRetention, PreRestoreRetention)
}
