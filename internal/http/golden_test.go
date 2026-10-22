package http

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// update rewrites the wire contract files the web app's tests read (#524):
// go test ./internal/http -run 'Wire|ErrorCodes' -update
var update = flag.Bool("update", false, "rewrite the wire samples and error-code list under web/src/test/wire")

// wireDir is where those files live, beside the Vitest code that imports them.
const wireDir = "../../web/src/test/wire"

// checkGolden compares got with the file at path, or writes it under -update.
// A mismatch fails with the command that regenerates it: the change may be
// intended, and then the web side's types and copy must follow in the same PR.
func checkGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading %s: %v (run: go test ./internal/http -run '%s' -update)", path, err, t.Name())
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is out of date with the server. If the change is intended, run\n\tgo test ./internal/http -run '%s' -update\nand bring the web app's types and copy along.\ngot:\n%s", path, t.Name(), got)
	}
}
