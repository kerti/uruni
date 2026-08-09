package main

import "testing"

func TestListenPortDefaultsAndValidates(t *testing.T) {
	t.Setenv("PORT", "")
	if got, err := listenPort(); err != nil || got != defaultPort {
		t.Errorf("listenPort() with PORT unset = (%d, %v), want (%d, nil)", got, err, defaultPort)
	}

	t.Setenv("PORT", "8099")
	if got, err := listenPort(); err != nil || got != 8099 {
		t.Errorf("listenPort() with PORT=8099 = (%d, %v), want (8099, nil)", got, err)
	}

	for _, bad := range []string{"delapan ribu", "0", "70000"} {
		t.Setenv("PORT", bad)
		if _, err := listenPort(); err == nil {
			t.Errorf("listenPort() with PORT=%q = nil error, want a refusal", bad)
		}
	}
}

func TestRunRejectsAnEmptyCommand(t *testing.T) {
	err := run(nil)
	if err == nil {
		t.Fatal("run(nil) = nil, want an error naming a subcommand")
	}
}

func TestRunRejectsAnUnknownCommand(t *testing.T) {
	err := run([]string{"migrasi"})
	if err == nil {
		t.Fatal("run([migrasi]) = nil, want an error")
	}
}
