package backup

import (
	"time"

	// Embeds the IANA time zone database into the binary. The runtime image
	// is gcr.io/distroless/static-debian12 (Dockerfile) - "static" ships no
	// /usr/share/zoneinfo at all (verified against the base image; only the
	// "base" distroless variants carry it), so time.LoadLocation below would
	// fail on every container boot without this import. ~450KB in the binary
	// is cheaper than adding tzdata to an image chosen specifically for
	// having no package manager to add it with.
	_ "time/tzdata"
)

// jakarta is the treasurer's calendar day (M6.38, #324): a dump's date is
// stamped in Asia/Jakarta, never the server's own clock, because a
// container almost always runs UTC and a dump taken before 07:00 WIB would
// otherwise stamp itself with the wrong (previous) day. Fixed rather than
// configurable - nothing in the PRD, CONTEXT.md or any ADR names a
// per-fund or per-instance time zone, and Uruni has no multi-region story
// yet; the day this needs revisiting is the day that changes, not before.
//
// Loaded once at package init. The only way LoadLocation fails once the
// tzdata import above is in place is a name absent from the IANA database,
// which "Asia/Jakarta" is not - so a failure here means the toolchain
// itself is broken, and panicking loudly beats silently mis-dating every
// backup as UTC forever.
var jakarta = mustLoadLocation("Asia/Jakarta")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("backup: loading time zone " + name + ": " + err.Error())
	}
	return loc
}
