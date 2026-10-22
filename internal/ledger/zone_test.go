package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
	"github.com/kerti/uruni/internal/tz"
)

// zoneLoc loads one of the fund's three zones for a test. Tests build their
// instants in an explicit zone and never in the machine's: CI runs in UTC, and a
// fixture that leaned on the local zone would pass on one machine and fail on
// the other.
func zoneLoc(tb testing.TB, name string) *time.Location {
	tb.Helper()
	loc, err := tz.Load(name)
	if err != nil {
		tb.Fatalf("tz.Load(%q): %v", name, err)
	}
	return loc
}

// wib is the default zone, for fixtures that name an instant by its wall clock
// (the other two zones are named by zoneLoc where a test needs them).
var wib = zoneLoc0(tz.Jakarta)

func zoneLoc0(name string) *time.Location {
	loc, err := tz.Load(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// setFundZone changes a fund's time zone straight through the query, as
// Pengaturan's PATCH /api/fund does.
func setFundZone(tb testing.TB, q *store.Queries, fundID int64, zone string) {
	tb.Helper()
	if _, err := q.UpdateFund(context.Background(), store.UpdateFundParams{ID: fundID, TimeZone: &zone}); err != nil {
		tb.Fatalf("UpdateFund(time_zone=%q) = %v, want no error", zone, err)
	}
}
