package backup

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/store"
)

// jakartaTime is a small test helper: a time.Time constructed directly in
// Asia/Jakarta, so a test that cares about the treasurer's calendar day
// never has to route its intent through a UTC offset by hand.
func jakartaTime(year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, jakarta)
}

// testDiscardLogger is the same recipe internal/http's own errors_test.go
// uses: a real *slog.Logger, writing nowhere, so EnsureBootDump's logging
// calls have somewhere to go without a test asserting on log lines.
func testDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBuildDumpNameAndParseDumpNameRoundTrip(t *testing.T) {
	when := jakartaTime(2026, 9, 30, 14, 5, 1)
	hash := "3f9a2c8e10b4deadbeef"
	name := BuildDumpName(when, KindDaily, 1, hash)

	want := "uruni-20260930-140501-daily-fv1-3f9a2c8e10b4.zip"
	if name != want {
		t.Fatalf("BuildDumpName() = %q, want %q", name, want)
	}

	info, ok := ParseDumpName(name)
	if !ok {
		t.Fatalf("ParseDumpName(%q) ok = false, want true", name)
	}
	if info.Kind != KindDaily {
		t.Errorf("Kind = %q, want %q", info.Kind, KindDaily)
	}
	if info.FormatVersion != 1 {
		t.Errorf("FormatVersion = %d, want 1", info.FormatVersion)
	}
	if info.Hash != hash[:12] {
		t.Errorf("Hash = %q, want %q", info.Hash, hash[:12])
	}
	if !info.When.Equal(when) {
		t.Errorf("When = %v, want %v", info.When, when)
	}
}

func TestParseDumpNameRejectsForeignAndTraversalNames(t *testing.T) {
	cases := []string{
		"",
		"uruni.json",
		"uruni-2026-09-30.zip", // the ad hoc /api/backup name (ZipFilename) - a different shape entirely
		".uruni-backup-tmp-123456.zip",
		"../etc/passwd",
		"uruni-20260930-140501-daily-fv1-3f9a2c8e10b4.zip/../../etc/passwd",
		"uruni-20260930-140501-weird-fv1-3f9a2c8e10b4.zip", // not a known kind
		"uruni-20260930-140501-daily-fvX-3f9a2c8e10b4.zip", // non-numeric format version
		"uruni-20260930-140501-daily-fv1-SHORT.zip",        // hash too short / not hex
		"uruni-2026093-140501-daily-fv1-3f9a2c8e10b4.zip",  // date wrong length
		"uruni-20260930-140501-daily-fv1-3f9a2c8e10b4.zip.bak",
	}
	for _, name := range cases {
		if _, ok := ParseDumpName(name); ok {
			t.Errorf("ParseDumpName(%q) ok = true, want false", name)
		}
		if ValidDumpName(name) {
			t.Errorf("ValidDumpName(%q) = true, want false", name)
		}
	}
}

func TestChangeHashSameInputsSameHash(t *testing.T) {
	docBytes := []byte(`{"format_version":1}`)
	h1 := ChangeHash(docBytes, []string{"b.jpg", "a.jpg"})
	h2 := ChangeHash(docBytes, []string{"a.jpg", "b.jpg"}) // order must not matter
	if h1 != h2 {
		t.Errorf("ChangeHash differs by missing-list order: %q vs %q", h1, h2)
	}
}

func TestChangeHashDiffersOnMissingReceiptChange(t *testing.T) {
	docBytes := []byte(`{"format_version":1}`)
	unchanged := ChangeHash(docBytes, nil)
	oneMissing := ChangeHash(docBytes, []string{"a.jpg"})
	if unchanged == oneMissing {
		t.Error("ChangeHash did not change when a receipt file went missing - a lost photo must count as a real change")
	}
}

func TestChangeHashDiffersOnDocBytesChange(t *testing.T) {
	h1 := ChangeHash([]byte(`{"a":1}`), nil)
	h2 := ChangeHash([]byte(`{"a":2}`), nil)
	if h1 == h2 {
		t.Error("ChangeHash did not change when docBytes changed")
	}
}

func writeDumpFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("stub"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestListDumpsSortsNewestFirstAndIgnoresForeignFiles(t *testing.T) {
	dir := t.TempDir()
	writeDumpFile(t, dir, "uruni-20260928-000000-daily-fv1-aaaaaaaaaaaa.zip")
	writeDumpFile(t, dir, "uruni-20260930-000000-daily-fv1-bbbbbbbbbbbb.zip")
	writeDumpFile(t, dir, "uruni-20260929-000000-pre-restore-fv1-cccccccccccc.zip")
	writeDumpFile(t, dir, "not-a-dump.txt")
	writeDumpFile(t, dir, ".uruni-backup-tmp-xyz.zip")

	dumps, err := ListDumps(dir)
	if err != nil {
		t.Fatalf("ListDumps() = %v, want no error", err)
	}
	if len(dumps) != 3 {
		t.Fatalf("len(dumps) = %d, want 3 (foreign files must be ignored): %+v", len(dumps), dumps)
	}
	if dumps[0].Name != "uruni-20260930-000000-daily-fv1-bbbbbbbbbbbb.zip" {
		t.Errorf("dumps[0] = %s, want the newest dump first", dumps[0].Name)
	}
	if dumps[2].Name != "uruni-20260928-000000-daily-fv1-aaaaaaaaaaaa.zip" {
		t.Errorf("dumps[2] = %s, want the oldest dump last", dumps[2].Name)
	}
}

func TestApplyRetentionKeepsOnlyEachKindsOwnQuotaAndTouchesNothingElse(t *testing.T) {
	dir := t.TempDir()
	// 4 daily dumps, keep 2; 3 pre-restore dumps, keep 1 - deliberately
	// smaller than the real DailyRetention/PreRestoreRetention constants so
	// the test proves the parameters are honoured, not just the defaults.
	dailyNames := []string{
		"uruni-20260901-000000-daily-fv1-111111111111.zip",
		"uruni-20260902-000000-daily-fv1-222222222222.zip",
		"uruni-20260903-000000-daily-fv1-333333333333.zip",
		"uruni-20260904-000000-daily-fv1-444444444444.zip",
	}
	preRestoreNames := []string{
		"uruni-20260901-010000-pre-restore-fv1-555555555555.zip",
		"uruni-20260902-010000-pre-restore-fv1-666666666666.zip",
		"uruni-20260903-010000-pre-restore-fv1-777777777777.zip",
	}
	for _, n := range dailyNames {
		writeDumpFile(t, dir, n)
	}
	for _, n := range preRestoreNames {
		writeDumpFile(t, dir, n)
	}
	const foreignName = "operators-own-file.txt"
	writeDumpFile(t, dir, foreignName)

	if err := ApplyRetention(dir, 2, 1); err != nil {
		t.Fatalf("ApplyRetention() = %v, want no error", err)
	}

	remaining, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() = %v, want no error", err)
	}
	names := map[string]bool{}
	for _, e := range remaining {
		names[e.Name()] = true
	}

	if !names[foreignName] {
		t.Errorf("foreign file %s was removed - retention must only ever touch its own dump files", foreignName)
	}
	for _, want := range []string{dailyNames[2], dailyNames[3]} {
		if !names[want] {
			t.Errorf("newest daily dump %s was removed, want it kept", want)
		}
	}
	for _, gone := range []string{dailyNames[0], dailyNames[1]} {
		if names[gone] {
			t.Errorf("old daily dump %s was kept, want it pruned past the quota", gone)
		}
	}
	if !names[preRestoreNames[2]] {
		t.Errorf("newest pre-restore dump %s was removed, want it kept", preRestoreNames[2])
	}
	for _, gone := range []string{preRestoreNames[0], preRestoreNames[1]} {
		if names[gone] {
			t.Errorf("old pre-restore dump %s was kept, want it pruned past its own separate quota", gone)
		}
	}
}

func TestNeedsFormatVersionDumpWhenNoDumpsExistYet(t *testing.T) {
	needed, err := NeedsFormatVersionDump(t.TempDir())
	if err != nil {
		t.Fatalf("NeedsFormatVersionDump() = %v, want no error", err)
	}
	if !needed {
		t.Error("needed = false with an empty backup directory, want true - a fresh instance has no restorable dump yet")
	}
}

func TestNeedsFormatVersionDumpWhenNewestMatchesCurrentVersion(t *testing.T) {
	dir := t.TempDir()
	writeDumpFile(t, dir, BuildDumpName(jakartaTime(2026, 9, 30, 1, 0, 0), KindDaily, FormatVersion, "aaaaaaaaaaaaaaaa"))

	needed, err := NeedsFormatVersionDump(dir)
	if err != nil {
		t.Fatalf("NeedsFormatVersionDump() = %v, want no error", err)
	}
	if needed {
		t.Error("needed = true when the newest dump already matches FormatVersion, want false")
	}
}

func TestNeedsFormatVersionDumpWhenNewestIsAnOlderFormat(t *testing.T) {
	dir := t.TempDir()
	writeDumpFile(t, dir, BuildDumpName(jakartaTime(2026, 9, 29, 1, 0, 0), KindDaily, FormatVersion, "aaaaaaaaaaaaaaaa"))
	writeDumpFile(t, dir, BuildDumpName(jakartaTime(2026, 9, 30, 1, 0, 0), KindDaily, FormatVersion-1, "bbbbbbbbbbbbbbbb"))

	needed, err := NeedsFormatVersionDump(dir)
	if err != nil {
		t.Fatalf("NeedsFormatVersionDump() = %v, want no error", err)
	}
	if !needed {
		t.Error("needed = false when the newest dump is an older format, want true")
	}
}

// TestWriteDumpWritesThenRenamesLeavingNoTempFile: after WriteDump returns,
// the backup directory holds exactly the finished dump under its own final
// name - no leftover ".uruni-backup-tmp-*" file from the write-then-rename
// step.
func TestWriteDumpWritesThenRenamesLeavingNoTempFile(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)

	name, err := WriteDump(ctx, q, l, uploadsDir, backupDir, KindDaily, jakartaTime(2026, 9, 30, 10, 0, 0))
	if err != nil {
		t.Fatalf("WriteDump() = %v, want no error", err)
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("ReadDir() = %v, want no error", err)
	}
	if len(entries) != 1 {
		t.Fatalf("backupDir has %d entries, want exactly 1: %v", len(entries), entries)
	}
	if entries[0].Name() != name {
		t.Errorf("backupDir's only entry is %q, want the returned name %q (a leftover temp file, or a mismatch)", entries[0].Name(), name)
	}
	if !ValidDumpName(name) {
		t.Errorf("WriteDump returned %q, which is not a valid dump name", name)
	}
}

func TestWriteDailyIfNeededSkipsWhenTodaysDumpAlreadyExists(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)

	now := jakartaTime(2026, 9, 30, 8, 0, 0)
	// A dump already exists for today, at an earlier hour, with a hash that
	// does not match the current fund state - proving the "today already
	// has one" check short-circuits before the hash is even compared.
	writeDumpFile(t, backupDir, BuildDumpName(jakartaTime(2026, 9, 30, 1, 0, 0), KindDaily, FormatVersion, "0000000000000000"))

	name, written, err := WriteDailyIfNeeded(ctx, q, l, uploadsDir, backupDir, now)
	if err != nil {
		t.Fatalf("WriteDailyIfNeeded() = %v, want no error", err)
	}
	if written {
		t.Errorf("written = true, want false (today already has a dump); name = %q", name)
	}

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("ReadDir() = %v, want no error", err)
	}
	if len(entries) != 1 {
		t.Errorf("backupDir has %d entries, want 1 (no new dump written)", len(entries))
	}
}

func TestWriteDailyIfNeededSkipsWhenFundUnchangedSinceLastDailyDump(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)

	yesterday := jakartaTime(2026, 9, 29, 3, 0, 0)
	firstName, written, err := WriteDailyIfNeeded(ctx, q, l, uploadsDir, backupDir, yesterday)
	if err != nil {
		t.Fatalf("WriteDailyIfNeeded(yesterday) = %v, want no error", err)
	}
	if !written {
		t.Fatal("written = false on the very first call, want true (backupDir was empty)")
	}

	today := jakartaTime(2026, 9, 30, 3, 0, 0)
	secondName, written, err := WriteDailyIfNeeded(ctx, q, l, uploadsDir, backupDir, today)
	if err != nil {
		t.Fatalf("WriteDailyIfNeeded(today) = %v, want no error", err)
	}
	if written {
		t.Errorf("written = true on an unchanged fund, want false; wrote %q on top of %q", secondName, firstName)
	}
}

func TestWriteDailyIfNeededWritesWhenFundChangedAcrossTheDayBoundary(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)

	yesterday := jakartaTime(2026, 9, 29, 3, 0, 0)
	if _, written, err := WriteDailyIfNeeded(ctx, q, l, uploadsDir, backupDir, yesterday); err != nil || !written {
		t.Fatalf("WriteDailyIfNeeded(yesterday) = (written=%v, err=%v), want (true, nil)", written, err)
	}

	// The fund changes: a fresh transaction, posted directly through
	// store.Queries (buildFixture's own pattern - see its doc comment on
	// why this package's fixtures avoid internal/ledger's time.Now() stamps),
	// against the one fund and account buildFixture already created.
	funds, err := q.ListFunds(ctx)
	if err != nil || len(funds) != 1 {
		t.Fatalf("ListFunds() = (%v, %v), want exactly 1 fund", funds, err)
	}
	accounts, err := q.ListAccountsByFund(ctx, funds[0].ID)
	if err != nil || len(accounts) == 0 {
		t.Fatalf("ListAccountsByFund() = (%v, %v), want at least 1 account", accounts, err)
	}
	purposes, err := q.ListPurposesByFund(ctx, funds[0].ID)
	if err != nil || len(purposes) == 0 {
		t.Fatalf("ListPurposesByFund() = (%v, %v), want at least 1 purpose", purposes, err)
	}
	if _, err := q.CreateTransaction(ctx, store.CreateTransactionParams{
		FundID: funds[0].ID, AccountID: accounts[0].ID,
		PurposeID: purposes[0].ID, Direction: "in", Amount: 12345,
		OccurredOn: "2026-09-30", Kind: "normal", CreatedAt: 1700000600,
	}); err != nil {
		t.Fatalf("CreateTransaction (fund change): %v", err)
	}

	today := jakartaTime(2026, 9, 30, 3, 0, 0)
	name, written, err := WriteDailyIfNeeded(ctx, q, l, uploadsDir, backupDir, today)
	if err != nil {
		t.Fatalf("WriteDailyIfNeeded(today) = %v, want no error", err)
	}
	if !written {
		t.Error("written = false after the fund changed, want true")
	}
	if !ValidDumpName(name) {
		t.Errorf("WriteDailyIfNeeded returned %q, which is not a valid dump name", name)
	}
}

// TestWriteDailyIfNeededDateIsAsiaJakartaAcrossTheUTCMidnightBoundary is the
// exact scenario the issue names: a container clock in UTC must still date
// the dump by the treasurer's own WIB day. 2026-09-29T18:00:00Z is
// 2026-09-30T01:00 WIB (UTC+7) - already the 30th in Jakarta, still the
// 29th in UTC.
func TestWriteDailyIfNeededDateIsAsiaJakartaAcrossTheUTCMidnightBoundary(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)

	utcJustBeforeJakartaMidnight := time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC)
	name, written, err := WriteDailyIfNeeded(ctx, q, l, uploadsDir, backupDir, utcJustBeforeJakartaMidnight)
	if err != nil {
		t.Fatalf("WriteDailyIfNeeded() = %v, want no error", err)
	}
	if !written {
		t.Fatal("written = false, want true (empty backupDir)")
	}

	info, ok := ParseDumpName(name)
	if !ok {
		t.Fatalf("ParseDumpName(%q) ok = false", name)
	}
	wantDate := "2026-09-30"
	gotDate := info.When.In(jakarta).Format("2006-01-02")
	if gotDate != wantDate {
		t.Errorf("dump dated %s (from a UTC clock reading %s), want %s - the treasurer's WIB day, not the server's UTC day",
			gotDate, utcJustBeforeJakartaMidnight.Format(time.RFC3339), wantDate)
	}

	// The complementary edge: a UTC time that is still the same day in WIB
	// too (23:00 WIB, same calendar date) must not roll over early either.
	utcStillSameJakartaDay := time.Date(2026, 9, 29, 16, 0, 0, 0, time.UTC) // 23:00 WIB, 2026-09-29
	backupDir2 := t.TempDir()
	name2, written2, err := WriteDailyIfNeeded(ctx, q, l, uploadsDir, backupDir2, utcStillSameJakartaDay)
	if err != nil {
		t.Fatalf("WriteDailyIfNeeded() (second) = %v, want no error", err)
	}
	if !written2 {
		t.Fatal("written = false, want true (empty backupDir2)")
	}
	info2, ok := ParseDumpName(name2)
	if !ok {
		t.Fatalf("ParseDumpName(%q) ok = false", name2)
	}
	if got := info2.When.In(jakarta).Format("2006-01-02"); got != "2026-09-29" {
		t.Errorf("dump dated %s, want 2026-09-29 (still the same WIB day)", got)
	}
}

func TestWritePreRestoreDumpWritesAndAppliesItsOwnQuota(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)

	var names []string
	for i := 0; i < PreRestoreRetention+2; i++ {
		when := jakartaTime(2026, 9, 20+i, 12, 0, 0)
		name, err := WritePreRestoreDump(ctx, q, l, uploadsDir, backupDir, when)
		if err != nil {
			t.Fatalf("WritePreRestoreDump() = %v, want no error", err)
		}
		names = append(names, name)
	}

	dumps, err := ListDumps(backupDir)
	if err != nil {
		t.Fatalf("ListDumps() = %v, want no error", err)
	}
	if len(dumps) != PreRestoreRetention {
		t.Fatalf("len(dumps) = %d, want %d (its own retention quota, applied after every write)", len(dumps), PreRestoreRetention)
	}
	for _, d := range dumps {
		if d.Kind != KindPreRestore {
			t.Errorf("dump %s has kind %q, want %q", d.Name, d.Kind, KindPreRestore)
		}
	}
	// The most recent write must have survived pruning.
	lastName := names[len(names)-1]
	found := false
	for _, d := range dumps {
		if d.Name == lastName {
			found = true
		}
	}
	if !found {
		t.Errorf("most recent pre-restore dump %q was pruned, want it kept", lastName)
	}
}

// TestRemoveStaleTempsClearsOnlyOwnTempFiles: a crash between write and
// rename leaves a tempPrefix file behind; the next boot removes it, and
// nothing else in the folder - not a real dump, not a foreign file, not a
// directory that happens to share the prefix.
func TestRemoveStaleTempsClearsOnlyOwnTempFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	write(tempPrefix + "123456")
	write(tempPrefix + "789")
	write("uruni-20260930-171544-daily-fv1-08e78d29f809.zip")
	write("notes.txt")
	if err := os.Mkdir(filepath.Join(dir, tempPrefix+"dir"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	removed, err := RemoveStaleTemps(dir)
	if err != nil {
		t.Fatalf("RemoveStaleTemps() = %v, want no error", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir() = %v", err)
	}
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	want := []string{tempPrefix + "dir", "notes.txt", "uruni-20260930-171544-daily-fv1-08e78d29f809.zip"}
	if strings.Join(left, ",") != strings.Join(want, ",") {
		t.Errorf("left = %v, want %v", left, want)
	}
}

func TestEnsureBootDumpWritesWhenNoneExistAndSkipsOnceCurrent(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)
	logger := testDiscardLogger()

	if err := EnsureBootDump(ctx, q, l, uploadsDir, backupDir, jakartaTime(2026, 9, 30, 0, 5, 0), logger); err != nil {
		t.Fatalf("EnsureBootDump() (first boot) = %v, want no error", err)
	}
	first, err := ListDumps(backupDir)
	if err != nil {
		t.Fatalf("ListDumps() = %v, want no error", err)
	}
	if len(first) != 1 {
		t.Fatalf("len(dumps) = %d after first boot, want 1", len(first))
	}

	// A second boot, format version unchanged: no second dump.
	if err := EnsureBootDump(ctx, q, l, uploadsDir, backupDir, jakartaTime(2026, 9, 30, 0, 10, 0), logger); err != nil {
		t.Fatalf("EnsureBootDump() (second boot) = %v, want no error", err)
	}
	second, err := ListDumps(backupDir)
	if err != nil {
		t.Fatalf("ListDumps() = %v, want no error", err)
	}
	if len(second) != 1 {
		t.Fatalf("len(dumps) = %d after second boot, want still 1 (format version unchanged)", len(second))
	}
}

func TestEnsureBootDumpWritesAgainWhenFormatVersionChanged(t *testing.T) {
	sqlDB := newTestDB(t)
	uploadsDir := t.TempDir()
	buildFixture(t, sqlDB, uploadsDir)
	backupDir := t.TempDir()
	ctx := context.Background()
	q := store.New(sqlDB)
	l := ledger.New(sqlDB)
	logger := testDiscardLogger()

	writeDumpFile(t, backupDir, BuildDumpName(jakartaTime(2026, 9, 29, 0, 0, 0), KindDaily, FormatVersion-1, "aaaaaaaaaaaaaaaa"))

	if err := EnsureBootDump(ctx, q, l, uploadsDir, backupDir, jakartaTime(2026, 9, 30, 0, 5, 0), logger); err != nil {
		t.Fatalf("EnsureBootDump() = %v, want no error", err)
	}

	dumps, err := ListDumps(backupDir)
	if err != nil {
		t.Fatalf("ListDumps() = %v, want no error", err)
	}
	if len(dumps) != 2 {
		t.Fatalf("len(dumps) = %d, want 2 (the old-format dump plus a fresh current-format one)", len(dumps))
	}
	if dumps[0].FormatVersion != FormatVersion {
		t.Errorf("newest dump's FormatVersion = %d, want %d", dumps[0].FormatVersion, FormatVersion)
	}
}
