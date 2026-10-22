package tz

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"time"
)

// The three zones are fixed offsets with no daylight saving, loaded from the
// embedded tzdata (the runtime image ships no zoneinfo).
func TestZonesAreFixedOffsets(t *testing.T) {
	t.Parallel()
	for name, hours := range map[string]int{Jakarta: 7, Makassar: 8, Jayapura: 9} {
		loc, err := Load(name)
		if err != nil {
			t.Fatalf("Load(%q): %v", name, err)
		}
		if loc.String() != name {
			t.Errorf("Load(%q).String() = %q", name, loc.String())
		}
		for _, month := range []time.Month{time.January, time.July} {
			_, offset := time.Date(2026, month, 15, 12, 0, 0, 0, loc).Zone()
			if offset != hours*3600 {
				t.Errorf("%s offset in %s = %d seconds, want %d", name, month, offset, hours*3600)
			}
		}
	}
}

func TestNamesAreWIBWITAWITInOrder(t *testing.T) {
	t.Parallel()
	want := []string{"Asia/Jakarta", "Asia/Makassar", "Asia/Jayapura"}
	if got := Names(); !slices.Equal(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	if Default != Jakarta {
		t.Fatalf("Default = %q, want %q", Default, Jakarta)
	}
}

// Names hands out a copy: a caller scribbling on it cannot change the list the
// rest of the process reads.
func TestNamesReturnsACopy(t *testing.T) {
	t.Parallel()
	got := Names()
	got[0] = "Mars/Olympus"
	if Names()[0] != Jakarta {
		t.Fatal("mutating Names()'s result changed the package's list")
	}
}

// Load accepts exactly the three names. Anything else is an error, including
// the empty string a missing backup field reads as and a real IANA zone that is
// not Indonesian: there is no fallback to UTC or WIB.
func TestLoadRefusesEverythingButTheThree(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "UTC", "Asia/Tokyo", "Asia/Pontianak", "asia/jakarta", "WIB", " Asia/Jakarta", "Not/A_Zone"} {
		if loc, err := Load(name); err == nil || loc != nil {
			t.Errorf("Load(%q) = %v, %v; want an error", name, loc, err)
		}
	}
}

// A name absent from the IANA database means a broken toolchain, and the
// loader panics rather than silently dating everything in UTC.
func TestMustLoadLocationPanicsOnAnUnknownZone(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Fatal("mustLoadLocation(unknown zone) did not panic")
		}
	}()
	mustLoadLocation("Not/A_Zone")
}

// The same instant is a different day in each zone: 22:30 WIB on 30 September
// is already 1 October in WIT. This is the whole reason the zone is a fact
// about the fund.
func TestTheSameInstantIsADifferentDayInEachZone(t *testing.T) {
	t.Parallel()
	wib, _ := Load(Jakarta)
	instant := time.Date(2026, time.September, 30, 22, 30, 0, 0, wib)
	for name, want := range map[string]string{Jakarta: "2026-09-30", Makassar: "2026-09-30", Jayapura: "2026-10-01"} {
		loc, _ := Load(name)
		if got := instant.In(loc).Format(time.DateOnly); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	// 23:30 WIB is already the next day in WITA too.
	instant = time.Date(2026, time.September, 30, 23, 30, 0, 0, wib)
	loc, _ := Load(Makassar)
	if got := instant.In(loc).Format(time.DateOnly); got != "2026-10-01" {
		t.Errorf("WITA at 23:30 WIB: %s, want 2026-10-01", got)
	}
}

// ADR-041: the list of zones lives in three places, the schema's CHECK, Names()
// and the web's options (web/src/lib/timeZones.ts). One test reads the other two
// copies and fails when any list, its order, or the default differs. No Vitest
// reaches out of web/, so the pin lives here.
func TestTimeZoneListsAgree(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(filepath.Join("..", "db", "migrations", "00001_schema.sql"))
	if err != nil {
		t.Fatalf("reading the schema: %v", err)
	}
	m := regexp.MustCompile(`(?s)time_zone\s+TEXT\s+NOT NULL DEFAULT '([^']+)'\s+CHECK \(time_zone IN \(([^)]*)\)\)`).FindSubmatch(schema)
	if m == nil {
		t.Fatal("fund.time_zone's column definition not found in 00001_schema.sql")
	}
	schemaDefault, schemaList := string(m[1]), quotedStrings(string(m[2]))

	webSrc, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "lib", "timeZones.ts"))
	if err != nil {
		t.Fatalf("reading the web zone list: %v", err)
	}
	wl := regexp.MustCompile(`TIME_ZONES = \[([^\]]*)\] as const`).FindSubmatch(webSrc)
	wd := regexp.MustCompile(`DEFAULT_TIME_ZONE: TimeZone = '([^']+)'`).FindSubmatch(webSrc)
	if wl == nil || wd == nil {
		t.Fatal("TIME_ZONES or DEFAULT_TIME_ZONE not found in web/src/lib/timeZones.ts")
	}
	webList, webDefault := quotedStrings(string(wl[1])), string(wd[1])

	if got := Names(); len(got) != 3 {
		t.Fatalf("Names() = %v, want exactly the three Indonesian zones", got)
	}
	if !slices.Equal(schemaList, Names()) {
		t.Errorf("schema CHECK list = %v, tz.Names() = %v", schemaList, Names())
	}
	if !slices.Equal(webList, Names()) {
		t.Errorf("web TIME_ZONES = %v, tz.Names() = %v", webList, Names())
	}
	for what, def := range map[string]string{"schema DEFAULT": schemaDefault, "web DEFAULT_TIME_ZONE": webDefault} {
		if def != Default {
			t.Errorf("%s = %q, tz.Default = %q", what, def, Default)
		}
	}
}

// quotedStrings returns the contents of every '...' in s, in order.
func quotedStrings(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}
