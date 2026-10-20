package http

import (
	"time"

	"github.com/kerti/uruni/internal/tz"
)

// wib is the fund's default zone as a location, for fixtures that name an
// instant by its wall clock. Tests never lean on the machine's zone: CI is UTC.
var wib = mustZone(tz.Jakarta)

func mustZone(name string) *time.Location {
	loc, err := tz.Load(name)
	if err != nil {
		panic(err)
	}
	return loc
}
