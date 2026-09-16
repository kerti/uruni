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
// language's own escape (`\u00a0` in TypeScript, a double-quoted `"\u2728"` in
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

// repoFiles lists the files git would add: everything tracked, plus everything
// untracked that is not gitignored. Enumerating the working tree instead would
// police files that are not the repo's - a gitignored settings.local.json, a
// scratch script - and fail on someone's machine for a file no one else has.
//
// The untracked half is deliberate, and the reason it is here is the whole
// point of the guard. "Not committed yet" is not the same as "not ours": a
// file just created is exactly the file most likely to carry a stray character
// and the one a local gate exists to catch. Enumerating only `git ls-files`
// let a new PaymentHistory.tsx carry a raw middle dot through a green
// `make check` twice and redden CI on the commit instead (#260).
// `--others --exclude-standard` is precisely "files git would add" - it adds
// that file and still excludes anything gitignored. Do not narrow this back to
// tracked files only.
func repoFiles(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, args := range [][]string{
		{"ls-files", "-z"},
		{"ls-files", "-z", "--others", "--exclude-standard"},
	} {
		// #nosec G204 - args is one of the two literal slices above, not user
		// input; the loop exists only so both enumerations share this body.
		out, err := exec.Command("git", args...).Output()
		if err != nil {
			t.Skipf("git %s unavailable (%v); nothing to enumerate", strings.Join(args, " "), err)
		}
		for _, p := range strings.Split(string(out), "\x00") {
			if p != "" {
				paths = append(paths, p)
			}
		}
	}
	return paths
}

// TestRepoFilesIncludesUntracked pins the half of the enumeration that is
// easiest to lose to a tidy-up: a file that exists but has never been
// committed must still be listed, or a new file skips the ASCII guard until
// the moment it is too late. See repoFiles.
func TestRepoFilesIncludesUntracked(t *testing.T) {
	const probe = "ascii_guard_untracked_probe.go"
	if err := os.WriteFile(probe, []byte("package uruni\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) = %v, want no error", probe, err)
	}
	t.Cleanup(func() {
		if err := os.Remove(probe); err != nil {
			t.Errorf("removing %s: %v", probe, err)
		}
	})

	for _, p := range repoFiles(t) {
		if filepath.Clean(p) == probe {
			return
		}
	}
	t.Errorf("repoFiles() does not list the untracked %s; a newly created file would skip the ASCII guard", probe)
}

func TestSourceFilesAreASCII(t *testing.T) {
	scanned := 0

	for _, path := range repoFiles(t) {
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
		// from user input; reading the repo's own files is the point.
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
