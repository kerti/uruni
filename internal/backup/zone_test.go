package backup

import (
	"time"

	"github.com/kerti/uruni/internal/tz"
)

// jakarta, makassar and jayapura are the fund's three zones as locations, for
// fixtures that name an instant by its wall clock. Tests never lean on the
// machine's zone: CI is UTC.
var (
	jakarta  = mustZone(tz.Jakarta)
	makassar = mustZone(tz.Makassar)
	jayapura = mustZone(tz.Jayapura)
)

func mustZone(name string) *time.Location {
	loc, err := tz.Load(name)
	if err != nil {
		panic(err)
	}
	return loc
}
