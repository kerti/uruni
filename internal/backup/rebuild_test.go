package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// Tests for #516's two halves in this package: the rebuild entry point over the
// restore core (RestoreForRebuild) and the file-level swap (ReplaceDatabase).

func docWithOneLogin(t *testing.T) Document {
	t.Helper()
	doc := minimalDocWithOneFund(t)
	//nolint:gosec // not a credential - a fixed test fixture value
	doc.Users = []User{{ID: 7, Email: "treasurer@example.org", PasswordHash: "argon2id$fake$hash", CreatedAt: 5}}
	return doc
}

// TestRestoreForRebuildCarriesTheLoginOver is the rebuild twin of
// TestRestoreUserRowSurvives: the in-app restore leaves the live login alone,
// the rebuild inserts the backup's, id and hash included, into the empty
// scratch database.
func TestRestoreForRebuildCarriesTheLoginOver(t *testing.T) {
	t.Parallel()
	scratch := newTestDB(t)
	q := store.New(scratch)
	ctx := context.Background()
	if _, err := q.CreateSession(ctx, store.CreateSessionParams{Token: "t", Data: []byte("d"), ExpiresAt: 9999999999}); err != nil {
		t.Fatal(err)
	}

	if _, err := RestoreForRebuild(ctx, scratch, t.TempDir(), ParsedUpload{Document: docWithOneLogin(t)}); err != nil {
		t.Fatalf("RestoreForRebuild() = %v, want no error", err)
	}

	users, err := q.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 1 || users[0].ID != 7 || users[0].Email != "treasurer@example.org" || users[0].PasswordHash != "argon2id$fake$hash" || users[0].CreatedAt != 5 {
		t.Errorf("ListUsers() = %+v, want the backup's login with its id and hash", users)
	}
	if funds, err := q.ListFunds(ctx); err != nil || len(funds) != 1 {
		t.Errorf("ListFunds() = (%v, %v), want the backup's fund", funds, err)
	}
	if _, err := q.GetSession(ctx, store.GetSessionParams{Token: "t"}); err == nil {
		t.Errorf("a session survived the rebuild")
	}
}

func TestRestoreForRebuildNeedsExactlyOneLogin(t *testing.T) {
	t.Parallel()
	for name, users := range map[string][]User{
		"zero": {},
		"two":  {{ID: 1, Email: "a@example.org", PasswordHash: "h", CreatedAt: 1}, {ID: 2, Email: "b@example.org", PasswordHash: "h", CreatedAt: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			scratch := newTestDB(t)
			doc := minimalDocWithOneFund(t)
			doc.Users = users

			_, err := RestoreForRebuild(context.Background(), scratch, t.TempDir(), ParsedUpload{Document: doc})
			if !errors.Is(err, ErrRebuildLogins) {
				t.Fatalf("RestoreForRebuild() = %v, want ErrRebuildLogins", err)
			}
			if funds, _ := store.New(scratch).ListFunds(context.Background()); len(funds) != 0 {
				t.Errorf("a refused rebuild wrote %d fund(s)", len(funds))
			}
		})
	}
}

// TestRestoreForRebuildKeepsTheTotalsProof: the rebuild runs the same proof the
// in-app restore does, and a mismatch rolls back everything, the login too.
func TestRestoreForRebuildKeepsTheTotalsProof(t *testing.T) {
	t.Parallel()
	scratch := newTestDB(t)
	doc := docWithOneLogin(t)
	doc.Totals.Funds[0].Balance = 999 // the fund holds no transaction, so its real balance is 0

	_, err := RestoreForRebuild(context.Background(), scratch, t.TempDir(), ParsedUpload{Document: doc})
	if !errors.Is(err, ErrTotalsMismatch) {
		t.Fatalf("RestoreForRebuild() = %v, want ErrTotalsMismatch", err)
	}
	q := store.New(scratch)
	if n, _ := q.CountUsers(context.Background()); n != 0 {
		t.Errorf("a rolled-back rebuild left %d login(s)", n)
	}
	if funds, _ := q.ListFunds(context.Background()); len(funds) != 0 {
		t.Errorf("a rolled-back rebuild left %d fund(s)", len(funds))
	}
}

// TestRestoreForRebuildNeverOverwritesALogin: a target that already has a login
// is a refusal, not a replacement - even one whose id and email clash with
// nothing in the backup, so no constraint would have caught it.
func TestRestoreForRebuildNeverOverwritesALogin(t *testing.T) {
	t.Parallel()
	scratch := newTestDB(t)
	//nolint:gosec // not a credential - a fixed test fixture value
	live, err := store.New(scratch).CreateUser(context.Background(), store.CreateUserParams{
		Email: "someone-else@example.org", PasswordHash: "argon2id$other$hash", CreatedAt: 1,
	})
	if err != nil {
		t.Fatalf("CreateUser() = %v", err)
	}

	_, err = RestoreForRebuild(context.Background(), scratch, t.TempDir(), ParsedUpload{Document: docWithOneLogin(t)})
	if !errors.Is(err, ErrRebuildLogins) {
		t.Fatalf("RestoreForRebuild() into a database with a login = %v, want ErrRebuildLogins", err)
	}
	users, listErr := store.New(scratch).ListUsers(context.Background())
	if listErr != nil || len(users) != 1 || users[0].Email != live.Email {
		t.Errorf("ListUsers() = (%v, %v), want the original login untouched", users, listErr)
	}
}

// Serial on purpose, no t.Parallel: it shrinks a package-level upload cap.
func TestParseUploadLabelsAnOversizeFileAsTooLargeNotTooManyEntries(t *testing.T) {
	orig := MaxUploadBytes
	MaxUploadBytes = 10
	t.Cleanup(func() { MaxUploadBytes = orig })

	_, err := parseUploadBytes(t, zipOf(t, minimalDocJSON(t, FormatVersion), nil))
	if !errors.Is(err, ErrUploadTooLarge) {
		t.Fatalf("ParseUpload() = %v, want ErrUploadTooLarge", err)
	}
	if errors.Is(err, ErrTooManyEntries) {
		t.Errorf("ParseUpload() also matches ErrTooManyEntries, want the byte cap labelled for what it is")
	}
}

func TestPreRebuildName(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 10, 14, 3, 15, 0, 0, time.UTC) // 10:15 in Jakarta
	if got, want := PreRebuildName("/data/uruni.db", "v0.9.0-alpha.3", when), "uruni.db.pre-v0.9.0-alpha.3-20261014-101500"; got != want {
		t.Errorf("PreRebuildName() = %q, want %q", got, want)
	}
	if got := PreRebuildName("/data/uruni.db", "../../etc", when); strings.Contains(got, "/") {
		t.Errorf("PreRebuildName() = %q, want a version that cannot carry a path", got)
	}
}

func TestRemoveRebuildScratchRemovesOnlyItsOwn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, n := range []string{RebuildScratchPrefix + "build.db", RebuildScratchPrefix + "build.db-wal", "uruni.db", "uruni.db.pre-x-1", ".uruni-backup-tmp-x"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n, err := RemoveRebuildScratch(dir)
	if err != nil || n != 2 {
		t.Fatalf("RemoveRebuildScratch() = (%d, %v), want two files removed", n, err)
	}
	for _, keep := range []string{"uruni.db", "uruni.db.pre-x-1", ".uruni-backup-tmp-x"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s was removed: %v", keep, err)
		}
	}
}

func TestCheckDatabaseOwner(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := CheckDatabaseOwner(filepath.Join(dir, "absent.db")); err != nil {
		t.Errorf("CheckDatabaseOwner(absent) = %v, want nil", err)
	}
	own := filepath.Join(dir, "own.db")
	if err := os.WriteFile(own, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDatabaseOwner(own); err != nil {
		t.Errorf("CheckDatabaseOwner(own file) = %v, want nil", err)
	}
	// A file owned by someone else cannot be made in a test that is not root;
	// what can be pinned is that the check reads the owner it compares.
	st := &syscall.Stat_t{}
	if err := syscall.Stat(own, st); err != nil || int(st.Uid) != os.Getuid() {
		t.Fatalf("test premise: the new file is owned by uid %d, process uid %d (%v)", st.Uid, os.Getuid(), err)
	}
}

// swapFiles lays out a directory with an old database, a scratch file and an
// aside path, each holding recognisable bytes.
func swapFiles(t *testing.T) (dbPath, scratch, aside string) {
	t.Helper()
	dir := t.TempDir()
	dbPath = filepath.Join(dir, "uruni.db")
	scratch = filepath.Join(dir, RebuildScratchPrefix+"ready.db")
	aside = filepath.Join(dir, "uruni.db.pre-v1-20261014-101500")
	if err := os.WriteFile(dbPath, []byte("OLD"), 0o640); err != nil { //nolint:gosec // a mode other than the default is the point: the swap must keep it
		t.Fatal(err)
	}
	if err := os.WriteFile(scratch, []byte("NEW"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dbPath, scratch, aside
}

func readString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // a test's own temp path
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReplaceDatabaseSwapsAndKeepsTheOldOneAside(t *testing.T) {
	t.Parallel()
	dbPath, scratch, aside := swapFiles(t)
	// Sidecars of the old file: a stale one next to the new file would be replayed into it.
	for _, s := range []string{"-wal", "-shm", "-journal"} {
		if err := os.WriteFile(dbPath+s, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := ReplaceDatabase(dbPath, scratch, aside, ReplaceHooks{}); err != nil {
		t.Fatalf("ReplaceDatabase() = %v, want no error", err)
	}
	if got := readString(t, dbPath); got != "NEW" {
		t.Errorf("database holds %q, want the new file", got)
	}
	if got := readString(t, aside); got != "OLD" {
		t.Errorf("aside holds %q, want the old file", got)
	}
	if _, err := os.Stat(scratch); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("scratch still exists after the rename (%v)", err)
	}
	for _, s := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(dbPath + s); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("old %s survived the swap (%v)", s, err)
		}
	}
	if info, err := os.Stat(dbPath); err != nil || info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v (%v), want the old file's 0640", info.Mode().Perm(), err)
	}
}

// TestReplaceDatabaseKeepsAnUncheckpointedWAL: a -wal that still holds frames
// is part of the old database, so it is kept beside the aside copy rather than
// deleted.
func TestReplaceDatabaseKeepsAnUncheckpointedWAL(t *testing.T) {
	t.Parallel()
	dbPath, scratch, aside := swapFiles(t)
	if err := os.WriteFile(dbPath+"-wal", []byte("FRAMES"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceDatabase(dbPath, scratch, aside, ReplaceHooks{}); err != nil {
		t.Fatalf("ReplaceDatabase() = %v", err)
	}
	if got := readString(t, aside+"-wal"); got != "FRAMES" {
		t.Errorf("aside -wal holds %q, want the old WAL kept", got)
	}
}

// TestReplaceDatabaseThatCannotRenameLeavesTheOldFile is the crash before the
// one rename: uruni.db is never absent and never half-written.
func TestReplaceDatabaseThatCannotRenameLeavesTheOldFile(t *testing.T) {
	t.Parallel()
	dbPath, scratch, aside := swapFiles(t)
	hooks := ReplaceHooks{Rename: func(_, _ string) error { return errors.New("power cut") }}

	if err := ReplaceDatabase(dbPath, scratch, aside, hooks); err == nil {
		t.Fatal("ReplaceDatabase() = nil, want the rename's failure")
	}
	if got := readString(t, dbPath); got != "OLD" {
		t.Errorf("database holds %q, want the old file intact", got)
	}
	if got := readString(t, scratch); got != "NEW" {
		t.Errorf("scratch holds %q, want it left for the caller to remove", got)
	}
}

func TestReplaceDatabaseNeverOverwritesAnExistingAside(t *testing.T) {
	t.Parallel()
	dbPath, scratch, aside := swapFiles(t)
	if err := os.WriteFile(aside, []byte("PRECIOUS"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ReplaceDatabase(dbPath, scratch, aside, ReplaceHooks{})
	if !errors.Is(err, ErrPreRebuildExists) {
		t.Fatalf("ReplaceDatabase() = %v, want ErrPreRebuildExists", err)
	}
	if got := readString(t, aside); got != "PRECIOUS" {
		t.Errorf("aside holds %q, want it untouched", got)
	}
	if got := readString(t, dbPath); got != "OLD" {
		t.Errorf("database holds %q, want it untouched", got)
	}
}

func TestReplaceDatabaseWithNoOldFileJustPlacesTheNewOne(t *testing.T) {
	t.Parallel()
	dbPath, scratch, aside := swapFiles(t)
	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceDatabase(dbPath, scratch, aside, ReplaceHooks{}); err != nil {
		t.Fatalf("ReplaceDatabase() = %v", err)
	}
	if got := readString(t, dbPath); got != "NEW" {
		t.Errorf("database holds %q, want the new file", got)
	}
	if _, err := os.Stat(aside); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an aside copy exists for a database that was never there (%v)", err)
	}
}
