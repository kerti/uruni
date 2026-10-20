// preupgrade.go is the raw-SQLite safety net under an upgrade (ADR-012): a
// backup zip cannot be restored onto an older binary and `migrate down` is no
// rollback, so before `serve` migrates on a new version it copies the database
// file as it is, in a form the previous binary opens. Like dumps.go it lives in
// URUNI_BACKUP_DIR, but it is a different kind of file with its own name
// pattern and quota, invisible to ListDumps and ApplyRetention and untouched
// by them.

package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/tz"
)

// PreUpgradeRetention is how many pre-upgrade snapshots are kept, newest
// first, like PreRestoreRetention for the restore safety net.
const PreUpgradeRetention = 3

// ErrPreUpgradeSnapshot wraps every failure to write the snapshot or record
// that it was written. serve turns it into a refusal to boot: migrating with
// no way back is worse than not starting.
var ErrPreUpgradeSnapshot = errors.New("backup: pre-upgrade snapshot failed")

// preUpgradeNamePattern is the one definition of a snapshot's filename, so
// the retention that deletes by it can match nothing else.
//
// uruni-pre-upgrade-<date>-<time>_from-<version>_to-<version>.sqlite, e.g.
// uruni-pre-upgrade-20261014-101500_from-v0.9.0-alpha.2_to-v0.9.0-alpha.3.sqlite
// (Asia/Jakarta, tz.Default: ADR-041). Both versions are in the name so an
// operator reads which file is "before the upgrade to X" without opening
// anything. cleanVersion keeps a version to [0-9A-Za-z.-], so the "_"
// separators are unambiguous and a name can neither hold a path separator
// nor end in .zip (dumpNamePattern's suffix).
var preUpgradeNamePattern = regexp.MustCompile(`^uruni-pre-upgrade-(\d{8})-(\d{6})_from-([0-9A-Za-z.-]+)_to-([0-9A-Za-z.-]+)\.sqlite$`)

// preUpgradeTempPrefix is tempPrefix plus a tag: RemoveStaleTemps sweeps an
// interrupted VACUUM INTO like any other interrupted write, while the tag says
// whose it was.
const preUpgradeTempPrefix = tempPrefix + "pre-upgrade-"

// bootMarkerName is the file in the backup directory that remembers which
// version last booted. Not a dump, not a snapshot: both listings skip it.
const bootMarkerName = "uruni-boot-version"

// unknownVersion stands in for the "from" of an instance that has data but no
// marker: the first release that wrote one was upgraded into by someone.
const unknownVersion = "unknown"

// maxVersionLen keeps a snapshot's name inside every filesystem's limit.
const maxVersionLen = 40

// PreUpgradeInfo is one snapshot's identity, parsed from its name alone.
type PreUpgradeInfo struct {
	Name string
	From string
	To   string
	When time.Time // Asia/Jakarta (tz.Default)
}

// operatorLocation is the zone of the two files an operator keeps for rolling
// back, the pre-upgrade snapshot and the old database set aside by a rebuild
// (ADR-041): Asia/Jakarta, whatever the fund's zone is. Each is written under
// the instance lock before migrations, on a database whose schema may predate
// the fund's time_zone column, so no fund can be read then; neither is a day of
// the fund's, and neither is read back as one. It is a function and not a
// variable so no zone is held in a package.
func operatorLocation() *time.Location {
	loc, err := tz.Load(tz.Default)
	if err != nil {
		panic("backup: loading the operator-file zone: " + err.Error())
	}
	return loc
}

// cleanVersion turns a version string into what a filename and the marker can
// carry: [0-9A-Za-z.-], at most maxVersionLen characters, never empty.
func cleanVersion(v string) string {
	v = strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '-':
			return r
		}
		return '-'
	}, strings.TrimSpace(v))
	if len(v) > maxVersionLen {
		v = v[:maxVersionLen]
	}
	if v == "" {
		return unknownVersion
	}
	return v
}

// BuildPreUpgradeName renders a snapshot's filename; see preUpgradeNamePattern.
func BuildPreUpgradeName(when time.Time, from, to string) string {
	t := when.In(operatorLocation())
	return fmt.Sprintf("uruni-pre-upgrade-%s-%s_from-%s_to-%s.sqlite", t.Format("20060102"), t.Format("150405"), cleanVersion(from), cleanVersion(to))
}

// ParsePreUpgradeName is BuildPreUpgradeName's inverse; ok is false for
// anything that does not fully match the pattern.
func ParsePreUpgradeName(name string) (PreUpgradeInfo, bool) {
	m := preUpgradeNamePattern.FindStringSubmatch(name)
	if m == nil {
		return PreUpgradeInfo{}, false
	}
	when, err := time.ParseInLocation("20060102-150405", m[1]+"-"+m[2], operatorLocation())
	if err != nil {
		return PreUpgradeInfo{}, false
	}
	return PreUpgradeInfo{Name: name, From: m[3], To: m[4], When: when}, true
}

// ListPreUpgradeSnapshots returns every snapshot in backupDir, newest first.
// Anything else (dumps, the marker, a temp file, a foreign file) is skipped.
func ListPreUpgradeSnapshots(backupDir string) ([]PreUpgradeInfo, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil, fmt.Errorf("backup: reading %s: %w", backupDir, err)
	}
	var snaps []PreUpgradeInfo
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		if info, ok := ParsePreUpgradeName(e.Name()); ok {
			snaps = append(snaps, info)
		}
	}
	sort.Slice(snaps, func(i, j int) bool {
		if !snaps[i].When.Equal(snaps[j].When) {
			return snaps[i].When.After(snaps[j].When)
		}
		return snaps[i].Name > snaps[j].Name
	})
	return snaps, nil
}

// ApplyPreUpgradeRetention deletes every snapshot beyond the newest keep. Like
// ApplyRetention it removes only names ListPreUpgradeSnapshots matched, never
// a glob.
func ApplyPreUpgradeRetention(backupDir string, keep int) error {
	snaps, err := ListPreUpgradeSnapshots(backupDir)
	if err != nil {
		return err
	}
	for i, s := range snaps {
		if i < keep {
			continue
		}
		if err := os.Remove(filepath.Join(backupDir, s.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backup: removing old pre-upgrade snapshot %s: %w", s.Name, err)
		}
	}
	return nil
}

// WritePreUpgradeSnapshot copies the live database into backupDir with VACUUM
// INTO and returns the new file's name. It is the reusable half (EnsurePreUpgradeSnapshot
// decides whether to call it): one consistent, defragmented SQLite file, WAL
// folded in, which the binary that wrote the database opens as it is.
//
// It goes through WriteFileAtomic, so the final name never points at a
// partial copy and a crash leaves only a swept temp file. SQLite writes the
// file itself, by path; the temp file WriteFileAtomic creates is empty, which
// VACUUM INTO accepts, and its fsync before the rename covers SQLite's bytes.
func WritePreUpgradeSnapshot(ctx context.Context, sqlDB *sql.DB, backupDir, from, to string, now time.Time) (string, error) {
	name := BuildPreUpgradeName(now, from, to)
	err := WriteFileAtomic(backupDir, preUpgradeTempPrefix+"*", name, func(w io.Writer) error {
		f, ok := w.(*os.File)
		if !ok {
			return fmt.Errorf("no temp file to copy into (%T)", w)
		}
		_, err := sqlDB.ExecContext(ctx, "VACUUM INTO ?", f.Name())
		return err
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPreUpgradeSnapshot, err)
	}
	return name, nil
}

// bootMarker is the content of bootMarkerName, two lines of "key value":
//
//	booted v0.9.0-alpha.2
//	snapshotted v0.9.0-alpha.3
//
// Booted is the last version whose boot fully succeeded (migrations, schema
// check and integrity check). Snapshotted is the version a snapshot was
// already taken for, or an empty database needed none: the run that follows a
// failed boot reads it and writes nothing again.
type bootMarker struct {
	Booted      string
	Snapshotted string
}

func readBootMarker(backupDir string) (bootMarker, error) {
	data, err := os.ReadFile(filepath.Join(backupDir, bootMarkerName)) //nolint:gosec // the operator-configured backup directory plus a constant name
	if errors.Is(err, os.ErrNotExist) {
		return bootMarker{}, nil
	}
	if err != nil {
		return bootMarker{}, fmt.Errorf("%w: reading %s: %w", ErrPreUpgradeSnapshot, bootMarkerName, err)
	}
	var m bootMarker
	for _, line := range strings.Split(string(data), "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch key {
		case "booted":
			m.Booted = cleanVersion(value)
		case "snapshotted":
			m.Snapshotted = cleanVersion(value)
		}
	}
	return m, nil
}

func writeBootMarker(backupDir string, m bootMarker) error {
	var b strings.Builder
	if m.Booted != "" {
		b.WriteString("booted " + m.Booted + "\n")
	}
	if m.Snapshotted != "" {
		b.WriteString("snapshotted " + m.Snapshotted + "\n")
	}
	if err := writeBytesAtomic(backupDir, tempPrefix+"marker-*", bootMarkerName, []byte(b.String())); err != nil {
		return fmt.Errorf("%w: writing %s: %w", ErrPreUpgradeSnapshot, bootMarkerName, err)
	}
	return nil
}

// hasSchema reports whether the database already holds any table or index: a
// file an older binary could have written. An unmigrated, empty database (a
// first run) has nothing to roll back to. Schema rather than rows, because
// what an old binary needs to open is the schema.
func hasSchema(ctx context.Context, sqlDB *sql.DB) (bool, error) {
	var n int
	if err := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\'`).Scan(&n); err != nil {
		return false, fmt.Errorf("%w: looking for existing data: %w", ErrPreUpgradeSnapshot, err)
	}
	return n > 0, nil
}

// EnsurePreUpgradeSnapshot is the guard every path that migrates takes first
// (serve, migrate up, create-user), after the database is open and before
// db.Up. running is the version about to migrate.
//
//   - running is the version that last booted: a plain restart, nothing written.
//   - A snapshot was already taken for running (a boot that failed after this
//     step, retried by Docker's restart policy): nothing written. Without
//     this every retry would add a copy and retention would evict the good
//     older ones.
//   - A snapshot was already taken for another version and nothing has booted
//     since (v1 booted, v2 failed, now v2-fix): nothing written either, only
//     the marker moves to running. No boot succeeded since that copy, so no
//     entries exist that it lacks, and a second copy would only push the
//     real v1 one out.
//   - Otherwise the version changed. If the database holds a schema, snapshot
//     it (from the marker's Booted, or "unknown" when there is no marker: the
//     instances upgrading into the first release that has this). An empty
//     database needs no snapshot. Either way the marker then records that
//     running is handled, before the caller migrates.
//
// A failure to write the snapshot or the marker is returned wrapped in
// ErrPreUpgradeSnapshot and the caller must not migrate. Retention runs last,
// after the marker is down, so a failure before that can never evict a copy.
// The marker's Booted moves only in MarkBooted, once a serve boot has
// succeeded.
func EnsurePreUpgradeSnapshot(ctx context.Context, sqlDB *sql.DB, backupDir, running string, now time.Time, logger *slog.Logger) error {
	return ensurePreUpgradeSnapshot(ctx, sqlDB, backupDir, running, now, logger, writeBootMarker)
}

// ensurePreUpgradeSnapshot is EnsurePreUpgradeSnapshot over an injectable marker
// writer, for the test that fails it after the snapshot is on disk.
func ensurePreUpgradeSnapshot(ctx context.Context, sqlDB *sql.DB, backupDir, running string, now time.Time, logger *slog.Logger, writeMarker func(string, bootMarker) error) error {
	running = cleanVersion(running)
	m, err := readBootMarker(backupDir)
	if err != nil {
		return err
	}
	if m.Booted == running || m.Snapshotted == running {
		return nil
	}
	if m.Snapshotted != "" {
		return writeMarker(backupDir, bootMarker{Booted: m.Booted, Snapshotted: running})
	}

	populated, err := hasSchema(ctx, sqlDB)
	if err != nil {
		return err
	}
	if !populated {
		return writeMarker(backupDir, bootMarker{Booted: m.Booted, Snapshotted: running})
	}

	from := m.Booted
	if from == "" {
		from = unknownVersion
	}
	// Free what an interrupted earlier attempt left before asking for the
	// space; nothing of this process is in flight yet.
	if _, err := RemoveStaleTemps(backupDir); err != nil {
		logger.Warn("backup: could not clear stale temp files", "error", err)
	}
	name, err := WritePreUpgradeSnapshot(ctx, sqlDB, backupDir, from, running, now)
	if err != nil {
		return err
	}
	logger.Info("wrote pre-upgrade snapshot", "name", name, "from", from, "to", running)
	if err := writeMarker(backupDir, bootMarker{Booted: m.Booted, Snapshotted: running}); err != nil {
		return err
	}
	// The snapshot and the marker are down; a failed prune only leaves an extra file.
	if err := ApplyPreUpgradeRetention(backupDir, PreUpgradeRetention); err != nil {
		logger.Warn("backup: could not prune old pre-upgrade snapshots", "error", err)
	}
	return nil
}

// MarkBooted records that running booted all the way through, which makes it
// the version a future boot is compared with and clears Snapshotted. Clearing
// matters: after an operator goes back to this version, a later retry of the
// failed one must snapshot again, because entries were made since. A no-op
// when the marker already says so, so a plain restart writes nothing.
//
// Only a serve boot calls it: "booted" means a server ran on this version.
// If the marker cannot be written, the old one is removed on a best-effort
// basis and the write error returned: a marker still saying "a snapshot was
// taken, nothing booted since" would, after entries were made, make the next
// upgrade skip its copy. No marker means a snapshot from "unknown", the safe
// side.
func MarkBooted(backupDir, running string) error {
	return markBooted(backupDir, running, writeBootMarker)
}

func markBooted(backupDir, running string, writeMarker func(string, bootMarker) error) error {
	running = cleanVersion(running)
	m, err := readBootMarker(backupDir)
	if err != nil {
		return err
	}
	if m.Booted == running && m.Snapshotted == "" {
		return nil
	}
	if err := writeMarker(backupDir, bootMarker{Booted: running}); err != nil {
		_ = os.Remove(filepath.Join(backupDir, bootMarkerName))
		return err
	}
	return nil
}
