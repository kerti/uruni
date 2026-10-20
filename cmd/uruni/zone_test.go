package main

import (
	"time"

	"github.com/kerti/uruni/internal/tz"
)

// wib is Asia/Jakarta, for fixtures that name an instant by its wall clock.
var wib = func() *time.Location {
	loc, err := tz.Load(tz.Jakarta)
	if err != nil {
		panic(err)
	}
	return loc
}()
