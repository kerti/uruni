package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// ADR-041 in the backup package: format 2 carries fund.time_zone, a dump's name
// carries a utc8/utc9 token outside WIB, "has today a dump" is read in the fund's
// zone, and the operator's files keep their Asia/Jakarta stamp. Instants are
// named by an explicit wall clock; CI is UTC.

func zoneTime(loc *time.Location, year int, month time.Month, day, hour, minute, second int) time.Time {
	return time.Date(year, month, day, hour, minute, second, 0, loc)
}

func setZone(t *testing.T, sqlDB *sql.DB, zone string) {
	t.Helper()
	q := store.New(sqlDB)
	funds, err := q.ListFunds(context.Background())
	if err != nil || len(funds) == 0 {
		t.Fatalf("ListFunds() = %v, %v", funds, err)
	}
	if _, err := q.UpdateFund(context.Background(), store.UpdateFundParams{ID: funds[0].ID, TimeZone: &zone}); err != nil {
		t.Fatalf("UpdateFund(time_zone=%q) = %v", zone, err)
	}
}

// touch writes an empty file under a dump name: ListDumps reads names alone.
func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// --- the dump name grammar ---------------------------------------------------

func TestDumpNameCarriesAZoneTokenOutsideWIB(t *testing.T) {
	t.Parallel()
	instant := zoneTime(jakarta, 2026, 10, 1, 22, 30, 5) // 23:30:05 WITA, 00:30:05 WIT on 2 October
	hash := "3f9a2c8e10b4deadbeef"
	cases := []struct {
		zone, want string
	}{
		{tz.Jakarta, "uruni-20261001-223005-daily-fv2-3f9a2c8e10b4.zip"},
		{tz.Makassar, "uruni-20261001-233005-utc8-daily-fv2-3f9a2c8e10b4.zip"},
		{tz.Jayapura, "uruni-20261002-003005-utc9-daily-fv2-3f9a2c8e10b4.zip"},
	}
	for _, tc := range cases {
		name := BuildDumpName(instant, tc.zone, KindDaily, 2, hash)
		if name != tc.want {
			t.Errorf("BuildDumpName(%s) = %q, want %q", tc.zone, name, tc.want)
		}
		if !ValidDumpName(name) {
			t.Errorf("ValidDumpName(%q) = false", name)
		}
		info, ok := ParseDumpName(name)
		if !ok {
			t.Fatalf("ParseDumpName(%q) ok = false", name)
		}
		// The parse reads the stamp in its own zone, so the instant is exact.
		if !info.When.Equal(instant) || info.Zone != tc.zone || info.Kind != KindDaily || info.FormatVersion != 2 {
			t.Errorf("ParseDumpName(%q) = %+v, want zone %s at the instant %v", name, info, tc.zone, instant)
		}
	}

	// The pre-restore kind carries the token too.
	if got := BuildDumpName(instant, tz.Jayapura, KindPreRestore, 2, hash); got != "uruni-20261002-003005-utc9-pre-restore-fv2-3f9a2c8e10b4.zip" {
		t.Errorf("BuildDumpName(pre-restore, WIT) = %q", got)
	}
}

// A name with no token is Asia/Jakarta, whenever it was written: every dump a
// WIB instance wrote before this change keeps its meaning.
func TestADumpNameWithNoTokenIsJakarta(t *testing.T) {
	t.Parallel()
	info, ok := ParseDumpName("uruni-20260930-233000-daily-fv1-3f9a2c8e10b4.zip")
	if !ok {
		t.Fatal("ParseDumpName(old WIB name) ok = false")
	}
	if info.Zone != tz.Jakarta || !info.When.Equal(time.Date(2026, 9, 30, 16, 30, 0, 0, time.UTC)) {
		t.Errorf("old name parsed as %+v, want Asia/Jakarta, 16:30Z", info)
	}
}

func TestOnlyUTC8AndUTC9AreZoneTokens(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"uruni-20261001-223005-utc7-daily-fv2-3f9a2c8e10b4.zip",  // WIB is the absence of a token
		"uruni-20261001-223005-utc10-daily-fv2-3f9a2c8e10b4.zip", // not an Indonesian zone
		"uruni-20261001-223005-UTC8-daily-fv2-3f9a2c8e10b4.zip",
		"uruni-20261001-223005-wita-daily-fv2-3f9a2c8e10b4.zip", // a file name is not copy
		"uruni-20261001-223005-daily-utc8-fv2-3f9a2c8e10b4.zip", // the token sits after the time
		"uruni-20261001-223005-utc8-utc9-daily-fv2-3f9a2c8e10b4.zip",
	} {
		if _, ok := ParseDumpName(name); ok || ValidDumpName(name) {
			t.Errorf("%q parses/validates, want it refused", name)
		}
	}
}

func TestBuildDumpNamePanicsOnAZoneOutsideTheThree(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("BuildDumpName(UTC) did not panic")
		}
	}()
	BuildDumpName(time.Now(), "UTC", KindDaily, 2, "abc")
}

// A zone change never re-dates an old file: a WIT instance that goes back to WIB
// keeps two stamps in two zones, and "newest first" follows the instants, not the
// wall clocks. Read in one zone, the WIT dump's 00:30 on 1 October would sort
// after the WIB dump's 23:00 on 30 September, though it was taken half an hour
// earlier.
func TestTheNewestOrderFollowsInstantsAcrossAZoneChange(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	earlier := BuildDumpName(zoneTime(jakarta, 2026, 9, 30, 22, 30, 0), tz.Jayapura, KindDaily, 2, "aaaaaaaaaaaaaaaa") // utc9, 20261001-003000
	later := BuildDumpName(zoneTime(jakarta, 2026, 9, 30, 23, 0, 0), tz.Jakarta, KindDaily, 2, "bbbbbbbbbbbbbbbb")     // no token, 20260930-230000
	touch(t, dir, earlier)
	touch(t, dir, later)

	dumps, err := ListDumps(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(dumps) != 2 || dumps[0].Name != later || dumps[1].Name != earlier {
		t.Errorf("ListDumps() = %v, want the 23:00 WIB dump first", dumps)
	}

	// Retention keeps the newest by the same order: with a quota of one, the
	// older instant goes whatever its wall clock says.
	if err := ApplyRetention(dir, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, later)); err != nil {
		t.Errorf("retention removed the newest dump: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, earlier)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("retention kept the older dump, stat err = %v", err)
	}
}

// --- writing dumps in the fund's zone ----------------------------------------

func TestWriteDumpIsStampedInTheFundsZone(t *testing.T) {
	t.Parallel()
	instant := time.Date(2026, 9, 29, 17, 0, 0, 0, time.UTC) // 00:00 WIB, 01:00 WITA, 02:00 WIT on 30 September
	for zone, wantPrefix := range map[string]string{
		tz.Jakarta:  "uruni-20260930-000000-daily-",
		tz.Makassar: "uruni-20260930-010000-utc8-daily-",
		tz.Jayapura: "uruni-20260930-020000-utc9-daily-",
	} {
		t.Run(zone, func(t *testing.T) {
			t.Parallel()
			sqlDB := newTestDB(t)
			buildFixture(t, sqlDB, t.TempDir())
			setZone(t, sqlDB, zone)
			name, written, err := WriteDailyIfNeeded(context.Background(), sqlDB, t.TempDir(), t.TempDir(), instant)
			if err != nil || !written {
				t.Fatalf("WriteDailyIfNeeded() = %q, %v, %v", name, written, err)
			}
			if !strings.HasPrefix(name, wantPrefix) {
				t.Errorf("dump name = %q, want prefix %q", name, wantPrefix)
			}
			wd, err := WriteDump(context.Background(), sqlDB, t.TempDir(), t.TempDir(), KindPreRestore, instant)
			if err != nil || !strings.HasPrefix(wd, strings.Replace(wantPrefix, "daily", "pre-restore", 1)) {
				t.Errorf("WriteDump(pre-restore) = %q, %v", wd, err)
			}
		})
	}
}

// Before any fund exists (the boot dump on a fresh instance) there is no zone to
// read, and the name is WIB's: no token.
func TestTheBootDumpBeforeSetupIsStampedInWIB(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	zone, err := InstanceZone(context.Background(), store.New(sqlDB))
	if err != nil || zone != tz.Default {
		t.Fatalf("InstanceZone(no fund) = %q, %v, want %q", zone, err, tz.Default)
	}
	name, err := WriteDump(context.Background(), sqlDB, t.TempDir(), t.TempDir(), KindDaily, zoneTime(jakarta, 2026, 9, 30, 10, 0, 0))
	if err != nil {
		t.Fatalf("WriteDump() = %v", err)
	}
	if strings.Contains(name, "utc") || !strings.HasPrefix(name, "uruni-20260930-100000-daily-fv2-") {
		t.Errorf("boot dump name = %q, want a token-less WIB name", name)
	}
}

func TestInstanceZoneIsTheFirstFundsAndRefusesABrokenOne(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	buildFixture(t, sqlDB, t.TempDir())
	setZone(t, sqlDB, tz.Makassar)
	if zone, err := InstanceZone(context.Background(), store.New(sqlDB)); err != nil || zone != tz.Makassar {
		t.Errorf("InstanceZone() = %q, %v, want %q", zone, err, tz.Makassar)
	}
}

// "Does today have its dump" compares the dump's instant with the current
// instant, both read in the fund's zone now. A dump taken at 22:30 WIB on 30
// September is 00:30 on 1 October to a WIT fund, so at 23:00 WIB it already has
// today's dump; for a WIB fund it is 30 September, and at 00:10 WIB on 1 October
// it has not.
func TestHasTodayGotItsDumpIsReadInTheFundsZone(t *testing.T) {
	t.Parallel()
	taken := zoneTime(jakarta, 2026, 9, 30, 22, 30, 0)
	cases := []struct {
		name        string
		zone        string
		existing    string // the dump on disk, by the zone it was stamped in
		now         time.Time
		wantWritten bool
	}{
		{"WIB fund, same WIB evening", tz.Jakarta, tz.Jakarta, zoneTime(jakarta, 2026, 9, 30, 23, 0, 0), false},
		{"WIB fund, after WIB midnight", tz.Jakarta, tz.Jakarta, zoneTime(jakarta, 2026, 10, 1, 0, 10, 0), true},
		{"WIT fund, the WIB dump is already WIT's today", tz.Jayapura, tz.Jakarta, zoneTime(jakarta, 2026, 9, 30, 23, 0, 0), false},
		{"WIT fund, before WIT midnight", tz.Jayapura, tz.Jakarta, zoneTime(jakarta, 2026, 9, 30, 21, 59, 0), true},
		{"WIB fund that was WIT: the evening dump is not today's", tz.Jakarta, tz.Jayapura, zoneTime(jakarta, 2026, 10, 1, 0, 10, 0), true},
		{"WIB fund that was WIT: still the same WIB day", tz.Jakarta, tz.Jayapura, zoneTime(jakarta, 2026, 9, 30, 23, 0, 0), false},
		{"WITA fund, WITA-stamped dump, next WITA day", tz.Makassar, tz.Makassar, zoneTime(jakarta, 2026, 10, 1, 0, 30, 0), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sqlDB := newTestDB(t)
			buildFixture(t, sqlDB, t.TempDir())
			dir := t.TempDir()
			touch(t, dir, BuildDumpName(taken, tc.existing, KindDaily, FormatVersion, "ffffffffffffffff"))

			// The fund's own hash differs from the placeholder, so the change check
			// never hides the day check.
			_, written, err := writeDailyIfNeeded(context.Background(), readDocumentFrom(sqlDB), assembleZip, nil, t.TempDir(), dir, tc.zone, tc.now)
			if err != nil {
				t.Fatalf("writeDailyIfNeeded() = %v", err)
			}
			if written != tc.wantWritten {
				t.Errorf("written = %v, want %v", written, tc.wantWritten)
			}
		})
	}
}

func TestWriteDailyIfNeededRefusesAZoneOutsideTheThree(t *testing.T) {
	t.Parallel()
	sqlDB := newTestDB(t)
	buildFixture(t, sqlDB, t.TempDir())
	if _, _, err := writeDailyIfNeeded(context.Background(), readDocumentFrom(sqlDB), assembleZip, nil, t.TempDir(), t.TempDir(), "UTC", time.Now()); err == nil {
		t.Error("writeDailyIfNeeded(zone=UTC) = nil error, want a refusal")
	}
	if _, err := writeDump(context.Background(), readDocumentFrom(sqlDB), assembleZip, t.TempDir(), t.TempDir(), "", KindDaily, time.Now()); err == nil {
		t.Error("writeDump(zone=\"\") = nil error, want a refusal")
	}
}

// --- the operator's files stay WIB --------------------------------------------

// The pre-upgrade snapshot and the old database a rebuild sets aside are written
// before any fund can be read, and are not days of the fund's: they keep
// Asia/Jakarta, whatever zone the instant arrives in.
func TestOperatorFilesAreStampedInWIBWhateverTheInstantsZone(t *testing.T) {
	t.Parallel()
	instant := zoneTime(jakarta, 2026, 10, 14, 23, 30, 0) // 01:30 WIT on the 15th
	if got := BuildPreUpgradeName(instant.In(jayapura), "v0.9.0", "v0.10.0"); got != "uruni-pre-upgrade-20261014-233000_from-v0.9.0_to-v0.10.0.sqlite" {
		t.Errorf("BuildPreUpgradeName = %q, want the WIB stamp", got)
	}
	if got := PreRebuildName("/data/uruni.db", "v0.10.0", instant.In(makassar)); got != "uruni.db.pre-v0.10.0-20261014-233000" {
		t.Errorf("PreRebuildName = %q, want the WIB stamp", got)
	}
	info, ok := ParsePreUpgradeName("uruni-pre-upgrade-20261014-233000_from-v0.9.0_to-v0.10.0.sqlite")
	if !ok || !info.When.Equal(instant) {
		t.Errorf("ParsePreUpgradeName = %+v, %v, want the WIB instant", info, ok)
	}
}

// --- format 2 carries the zone --------------------------------------------------

func TestExportCarriesTheFundsZoneAndItRoundTripsThroughRestore(t *testing.T) {
	t.Parallel()
	for _, zone := range []string{tz.Makassar, tz.Jayapura} {
		t.Run(zone, func(t *testing.T) {
			t.Parallel()
			srcDB := newTestDB(t)
			srcUploads := t.TempDir()
			buildFixture(t, srcDB, srcUploads)
			if err := os.WriteFile(filepath.Join(srcUploads, receiptFilename), realJPEGBytes(t), 0o600); err != nil {
				t.Fatalf("replacing the fixture receipt with a real image: %v", err)
			}
			setZone(t, srcDB, zone)
			zipBytes, doc := exportFixture(t, srcDB, srcUploads)
			if doc.Funds[0].TimeZone != zone {
				t.Fatalf("exported fund time_zone = %q, want %q", doc.Funds[0].TimeZone, zone)
			}

			parsed, err := parseUploadBytes(t, zipBytes)
			if err != nil {
				t.Fatalf("ParseUpload() = %v, want no error", err)
			}
			destDB := newTestDB(t)
			restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)

			funds, err := store.New(destDB).ListFunds(context.Background())
			if err != nil || len(funds) != 1 || funds[0].TimeZone != zone {
				t.Errorf("restored funds = %+v, %v, want one fund in %q", funds, err, zone)
			}
			got, _, err := BuildDocument(context.Background(), destDB)
			if err != nil {
				t.Fatal(err)
			}
			assertDocumentsEqual(t, got, doc)
		})
	}
}

// docWithZone is a valid v2 document whose one fund carries zone.
func docWithZone(t *testing.T, zone string) Document {
	t.Helper()
	doc := minimalDocWithOneFund(t)
	doc.Funds[0].TimeZone = zone
	return doc
}

func zipOfDocument(t *testing.T, doc Document) []byte {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return zipOf(t, raw, nil)
}

// ParseUpload refuses a zone tz.Load refuses, the empty string a missing v2 field
// reads as included, at inspect (the preview cannot date a file without one).
func TestParseUploadRefusesAZoneOutsideTheThree(t *testing.T) {
	t.Parallel()
	for _, zone := range []string{"", "UTC", "Asia/Tokyo", "WIB", "asia/jakarta"} {
		if _, err := parseUploadBytes(t, zipOfDocument(t, docWithZone(t, zone))); !errors.Is(err, ErrMalformedBackup) {
			t.Errorf("ParseUpload(time_zone=%q) = %v, want ErrMalformedBackup", zone, err)
		}
	}
	for _, zone := range tz.Names() {
		if _, err := parseUploadBytes(t, zipOfDocument(t, docWithZone(t, zone))); err != nil {
			t.Errorf("ParseUpload(time_zone=%q) = %v, want no error", zone, err)
		}
	}
}

// A v2 file whose fund has no time_zone key at all is the same refusal.
func TestParseUploadRefusesAV2FundWithNoTimeZoneKey(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(docWithZone(t, tz.Jakarta))
	if err != nil {
		t.Fatal(err)
	}
	stripped := strings.Replace(string(raw), `"time_zone":"Asia/Jakarta",`, "", 1)
	if stripped == string(raw) {
		t.Fatal("fixture: the time_zone key was not found to strip")
	}
	if _, err := parseUploadBytes(t, zipOf(t, []byte(stripped), nil)); !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("ParseUpload(v2 without time_zone) = %v, want ErrMalformedBackup", err)
	}
}

// The schema's CHECK is the backstop behind ParseUpload: a document that never
// went through it (a hand-built one) is refused by the database and leaves it
// as it was.
func TestRestoreRefusesAZoneOutsideTheThreeThroughTheCheck(t *testing.T) {
	t.Parallel()
	destDB := newTestDB(t)
	buildFixture(t, destDB, t.TempDir())
	before, _, err := BuildDocument(context.Background(), destDB)
	if err != nil {
		t.Fatal(err)
	}

	bad := docWithZone(t, "Asia/Tokyo")
	path := filepath.Join(t.TempDir(), "x.zip")
	if err := os.WriteFile(path, zipOfDocument(t, bad), 0o600); err != nil {
		t.Fatal(err)
	}
	err = Restore(context.Background(), destDB, t.TempDir(), t.TempDir(), ParsedUpload{Document: bad, StagingPath: path}, time.Unix(1800000000, 0))
	if err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("Restore(time_zone=Asia/Tokyo) = %v, want the CHECK to refuse it", err)
	}
	after, _, err := BuildDocument(context.Background(), destDB)
	if err != nil {
		t.Fatal(err)
	}
	assertDocumentsEqual(t, after, before)
}

// A v1 backup restores as WIB: the v1 -> v2 step fills Asia/Jakarta, and a fund
// that was WIT before the restore is WIB after, as it carries the file's name and
// report link with it (ADR-041).
func TestAVersion1BackupRestoresAsWIB(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	buildFixture(t, srcDB, t.TempDir())
	_, doc := exportFixture(t, srcDB, t.TempDir())

	parsed, err := parseUploadBytes(t, v1Zip(t, v1Of(doc)))
	if err != nil {
		t.Fatalf("ParseUpload(v1) = %v, want no error", err)
	}
	if got := parsed.Document.Funds[0].TimeZone; got != tz.Jakarta {
		t.Fatalf("upgraded fund time_zone = %q, want Asia/Jakarta", got)
	}

	destDB := newTestDB(t)
	buildFixture(t, destDB, t.TempDir())
	setZone(t, destDB, tz.Jayapura)
	restoreOrFatal(t, destDB, t.TempDir(), t.TempDir(), parsed)
	funds, err := store.New(destDB).ListFunds(context.Background())
	if err != nil || len(funds) != 1 || funds[0].TimeZone != tz.Jakarta {
		t.Errorf("fund after restoring a v1 file = %+v, %v, want WIB", funds, err)
	}
}

// A v1 file carrying a time_zone is not a v1 file: the field did not exist.
func TestAVersion1FileCannotCarryATimeZone(t *testing.T) {
	t.Parallel()
	srcDB := newTestDB(t)
	buildFixture(t, srcDB, t.TempDir())
	_, doc := exportFixture(t, srcDB, t.TempDir())
	raw, _ := json.Marshal(v1Of(doc))
	withZone := strings.Replace(string(raw), `"currency":"IDR",`, `"currency":"IDR","time_zone":"Asia/Jakarta",`, 1)
	if withZone == string(raw) {
		t.Fatal("fixture: no currency key to extend")
	}
	if _, err := parseUploadBytes(t, zipOf(t, []byte(withZone), nil)); !errors.Is(err, ErrMalformedBackup) {
		t.Errorf("ParseUpload(v1 with time_zone) = %v, want ErrMalformedBackup", err)
	}
}
