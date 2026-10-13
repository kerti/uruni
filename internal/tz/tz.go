// Package tz holds the one time zone Uruni reckons the treasurer's calendar
// day in, so internal/backup (a dump's date) and internal/ledger (the public
// report's "current month", ADR-035) share one definition.
package tz

import (
	"time"

	// Embeds the IANA time zone database. The runtime image is
	// gcr.io/distroless/static-debian12, and "static" ships no /usr/share/zoneinfo
	// (only the "base" variants do), so time.LoadLocation below would fail on
	// every container boot without this. ~450KB beats adding tzdata to an image
	// chosen for having no package manager.
	_ "time/tzdata"
)

// Jakarta is the treasurer's calendar day (#324): a dump's date and the
// report's current month are stamped in Asia/Jakarta, never the server's
// clock, since a container usually runs UTC and a dump taken before 07:00 WIB
// would stamp the previous day. Fixed, not configurable: nothing in the PRD,
// CONTEXT.md or an ADR names a per-fund time zone, and there is no
// multi-region story yet.
//
// Loaded once at init. With the tzdata import, LoadLocation fails only on a
// name absent from the IANA database, which "Asia/Jakarta" is not: a failure
// means a broken toolchain, and panicking beats mis-dating every backup as UTC.
var Jakarta = mustLoadLocation("Asia/Jakarta")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("tz: loading time zone " + name + ": " + err.Error())
	}
	return loc
}
