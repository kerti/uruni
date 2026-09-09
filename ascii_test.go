package uruni

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Source files are ASCII. This is not typography policing: a single multi-byte
// character in a SQL query file made `sqlc generate` silently truncate several
// *unrelated* queries elsewhere in the same file - `GROUP BY dues_period`
// became `GROUP BY dues_perio`, `kind = 'opening'` became `kind = 'opening` -
// because the generator addresses its input by byte offset while counting in
// runes. Nothing failed loudly; the code compiled and the queries were wrong.
// internal/store/queries_ascii_test.go caught it for the query files, after
// generation. This is that guard widened to every file a tool parses.
//
// A character that must reach a human is still allowed: write it as the
// language's own escape (` ` in TypeScript, a double-quoted `"✨"` in
// YAML), which keeps the bytes ASCII and the rendered output identical. See
// CLAUDE.md, "Source files are ASCII".
//
// exemptPaths are the files whose *content* is text for humans rather than
// instructions for a tool.
var exemptPaths = map[string]bool{
	// Third-party legal text. Never reformatted, by anyone, for any reason.
	"LICENSE": true,
	"NOTICE":  true,
	// The one designated copy surface (CLAUDE.md rule 8). Indonesian
	// typography - a real ellipsis in "Membuka.\u2026", an em dash in body copy -
	// is the treasurer's reading experience, and no generator parses this file.
	filepath.Join("web", "src", "copy", "id.ts"): true,
}

// scannedExts are the files something parses: a compiler, a generator, a
// linter, a shell, a YAML reader.
var scannedExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".sql": true, ".sh": true, ".yml": true, ".yaml": true, ".json": true,
	".css": true, ".html": true,
}

// scannedNames are extensionless files in the same category.
var scannedNames = map[string]bool{
	"Makefile": true, "Dockerfile": true, "Caddyfile": true,
	"pre-commit": true, "pre-push": true,
}

// skipDirs are build output, dependencies and VCS internals - none of it ours.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "coverage": true,
	"playwright-report": true, "test-results": true, "docs": true,
}

// trackedFiles lists what git knows about. Enumerating the working tree
// instead would police files that are not the repo's - a gitignored
// settings.local.json, a scratch script - and fail on someone's machine for a
// file no one else has.
func trackedFiles(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files unavailable (%v); nothing to enumerate", err)
	}
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

func TestSourceFilesAreASCII(t *testing.T) {
	scanned := 0

	for _, path := range trackedFiles(t) {
		name := filepath.Base(path)
		if !scannedExts[filepath.Ext(name)] && !scannedNames[name] {
			continue
		}
		if exemptPaths[filepath.Clean(path)] || strings.HasSuffix(name, ".md") {
			continue
		}
		if top := strings.SplitN(path, "/", 2)[0]; skipDirs[top] {
			continue
		}

		// #nosec G304 - the path comes from `git ls-files` in this repo, not
		// from user input; reading the repo's own tracked files is the point.
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		scanned++

		for i, b := range src {
			if b < utf8.RuneSelf {
				continue
			}
			line := 1 + strings.Count(string(src[:i]), "\n")
			r, _ := utf8.DecodeRune(src[i:])
			t.Errorf("%s:%d contains a non-ASCII character %q (U+%04X). "+
				"Source files are ASCII - a byte like this made sqlc truncate unrelated queries once. "+
				"If it must reach a human, write it as an escape (\\u%04X) instead; if it is decoration, use ASCII.",
				path, line, r, r, r)
			break // one report per file is enough to find it
		}
	}

	// A guard watching nothing passes forever. This has caught a real bug
	// once; it should fail loudly if the enumeration ever stops finding files.
	if scanned < 50 {
		t.Fatalf("scanned only %d files, want the guard to be watching the whole repo", scanned)
	}
}
