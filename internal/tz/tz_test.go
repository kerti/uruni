package tz

import (
	"testing"
	"time"
)

// Jakarta is a fixed UTC+7 with no daylight saving, loaded from the embedded
// tzdata (the runtime image ships no zoneinfo).
func TestJakartaIsUTCPlusSeven(t *testing.T) {
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
