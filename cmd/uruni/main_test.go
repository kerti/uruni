package main

import (
	"errors"
	"testing"
)

func TestListenPortDefaultsAndValidates(t *testing.T) {
	t.Setenv("PORT", "")
	if got, err := listenPort(); err != nil || got != defaultPort {
		t.Errorf("listenPort() with PORT unset = (%d, %v), want (%d, nil)", got, err, defaultPort)
	}

	t.Setenv("PORT", "8099")
	if got, err := listenPort(); err != nil || got != 8099 {
		t.Errorf("listenPort() with PORT=8099 = (%d, %v), want (8099, nil)", got, err)
	}

	for _, bad := range []string{"eight thousand", "0", "70000"} {
		t.Setenv("PORT", bad)
		if _, err := listenPort(); !errors.Is(err, ErrInvalidPort) {
			t.Errorf("listenPort() with PORT=%q = %v, want ErrInvalidPort", bad, err)
		}
	}
}

func TestRunRejectsAnEmptyCommand(t *testing.T) {
	if err := run(nil); !errors.Is(err, ErrNoCommand) {
		t.Fatalf("run(nil) = %v, want ErrNoCommand", err)
	}
}

func TestRunRejectsAnUnknownCommand(t *testing.T) {
	if err := run([]string{"migrate-everything"}); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("run([migrate-everything]) = %v, want ErrUnknownCommand", err)
	}
}
