package store

import (
	"embed"
	"io/fs"
	"path/filepath"
	"testing"
	"unicode/utf8"
)

// Embedded rather than read from disk so the guard needs no path handling and
// fails at compile time if the directory ever moves.
//
//go:embed queries/*.sql
var queryFiles embed.FS

// TestQueryFilesAreASCII guards a silent code-generation bug rather than a
// style rule. sqlc v1.31.1's SQLite engine measures a statement's end in runes
// but slices the source in bytes, so every non-ASCII character in a query file
// chops one extra byte off the tail of the generated SQL constant - with no
// error, and a green build.
//
// Observed: three em dashes in ping.sql's comment turned `SELECT 1 AS ok` into
// `SELECT 1`, which still runs; one em dash above GetEffectiveDuesRate turned
// `LIMIT 1` into `LIMIT`, which does not. The failure mode scales with how
// much prose sits above the query, so the rule is the whole file, not the
// comment. Write "-" and "--", not an em dash; spell out sections rather than
// typing a section sign.
func TestQueryFilesAreASCII(t *testing.T) {
	paths, err := fs.Glob(queryFiles, "queries/*.sql")
	if err != nil {
		t.Fatalf("Glob() = %v, want no error", err)
	}
	if len(paths) == 0 {
		t.Fatal("no query files found, want the guard to be watching something")
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := queryFiles.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%s) = %v, want no error", path, err)
			}
			for i, b := range src {
				if b >= utf8.RuneSelf {
					line := 1
					for _, c := range src[:i] {
						if c == '\n' {
							line++
						}
					}
					t.Fatalf("%s:%d contains a non-ASCII byte %#x; it will truncate the generated SQL constant", path, line, b)
				}
			}
		})
	}
}
