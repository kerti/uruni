// Package tz is the vocabulary of the fund's time zone (ADR-041): the three
// Indonesian zones a fund can keep its calendar in, and the one way to turn a
// stored name into a *time.Location. A day or a month in Uruni is an injected
// instant read in the fund's zone, never the server's clock (a container runs
// UTC, so the host's reading puts a treasurer's evening in the wrong day and, on
// a month's last evening, in the wrong month: #324, #379) and never the
// device's.
//
// The package exports names, not locations: there is no *time.Location variable
// to reach for, so a stray now.In(tz.Jakarta) is a compile error and every
// caller has to come by the fund's zone honestly.
package tz

import (
	"fmt"
	"time"

	// Embeds the IANA time zone database. The runtime image is
	// gcr.io/distroless/static-debian12, and "static" ships no /usr/share/zoneinfo
	// (only the "base" variants do), so time.LoadLocation below would fail on
	// every container boot without this. ~450KB beats adding tzdata to an image
	// chosen for having no package manager.
	_ "time/tzdata"
)

// The three zones, by IANA name: WIB, WITA and WIT. Go's time.LoadLocation and
// the browser's Intl.DateTimeFormat take these as they are; the abbreviations
// exist only as copy (web/src/copy/id.ts). None observes daylight saving, which
// ADR-041 leans on: a zone's offset is constant.
const (
	Jakarta  = "Asia/Jakarta"
	Makassar = "Asia/Makassar"
	Jayapura = "Asia/Jayapura"
)

// Default is WIB. It is read only where a request omits the zone (Setup, the
// v1-to-v2 backup step) or where no fund can be read (the boot dump before
// setup, the two operator-file stamps). It never dates a day of a fund that
// exists.
const Default = Jakarta

// names is the closed list, in WIB, WITA, WIT order.
var names = [...]string{Jakarta, Makassar, Jayapura}

// locations holds each zone, loaded once at init. With the tzdata import,
// LoadLocation fails only on a name absent from the IANA database, which these
// are not: a failure means a broken toolchain, and panicking beats silently
// dating every day and backup in UTC.
var locations = func() map[string]*time.Location {
	m := make(map[string]*time.Location, len(names))
	for _, n := range names {
		m[n] = mustLoadLocation(n)
	}
	return m
}()

// Names returns the three zone names in WIB, WITA, WIT order. The schema's
// CHECK and the web's list are the other two copies; TestTimeZoneListsAgree
// pins all three together.
func Names() []string {
	return append([]string(nil), names[:]...)
}

// Load returns the location for one of the three names, and an error for
// anything else, the empty string included. It never falls back: a name outside
// the list cannot get past the schema's CHECK, so one reaching here means the
// database is broken, and the caller answers 500 rather than dating a day in
// UTC or WIB.
func Load(name string) (*time.Location, error) {
	if loc, ok := locations[name]; ok {
		return loc, nil
	}
	return nil, fmt.Errorf("tz: %q is not one of %v", name, names)
}

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("tz: loading time zone " + name + ": " + err.Error())
	}
	return loc
}
