package main

import (
	"fmt"
	"io"
	"runtime/debug"
)

// version and commit are stamped at link time by the Dockerfile's VERSION and
// COMMIT build-args, which release.yml fills from the pushed tag and SHA
// (ADR-018 makes the version the operator's half of the upgrade contract, so an
// image reporting `dev` for a tagged release makes it unverifiable).
//
// They are variables because -ldflags -X can only write a variable; nothing
// else assigns them.
var (
	version = "dev"
	commit  = ""
)

// unknownCommit is what a build with neither a stamp nor a readable VCS record
// reports: honest, not a plausible-looking blank.
const unknownCommit = "unknown"

// printVersion writes the one line an operator reads to decide whether an
// upgrade is drop-in (ADR-018).
func printVersion(w io.Writer) error {
	_, err := fmt.Fprintf(w, "uruni %s (commit %s)\n", version, buildCommit())
	return err
}

// buildCommit prefers the linker stamp and falls back to Go's VCS stamping.
// Both are needed: .dockerignore keeps .git out of the image build, so the
// image needs the stamp; a local `go build` has no stamp but has .git.
func buildCommit() string {
	if commit != "" {
		return shortCommit(commit)
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return unknownCommit
	}

	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision == "" {
		// `go test` and `go run` produce binaries with no VCS record at all.
		return unknownCommit
	}
	if modified == "true" {
		// Uncommitted changes: the revision alone would name a tree that isn't running.
		return shortCommit(revision) + "-dirty"
	}
	return shortCommit(revision)
}

// shortCommit abbreviates to git's usual seven characters: enough to look up,
// short enough to read over a phone call.
func shortCommit(sha string) string {
	const short = 7
	if len(sha) <= short {
		return sha
	}
	return sha[:short]
}
