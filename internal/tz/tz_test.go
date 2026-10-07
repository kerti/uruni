package tz

import (
	"testing"
	"time"
)

// Jakarta is a fixed UTC+7 with no daylight saving, loaded from the embedded
// tzdata (the runtime image ships no zoneinfo).
func TestJakartaIsUTCPlusSeven(t *testing.T) {
	t.Parallel()
	if Jakarta.String() != "Asia/Jakarta" {
		t.Fatalf("Jakarta = %q, want Asia/Jakarta", Jakarta.String())
	}
	for _, month := range []time.Month{time.January, time.July} {
		_, offset := time.Date(2026, month, 15, 12, 0, 0, 0, Jakarta).Zone()
		if offset != 7*3600 {
			t.Errorf("offset in %s = %d seconds, want %d", month, offset, 7*3600)
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
