// Command uruni-notices writes THIRD-PARTY-NOTICES (#521): the licence text and
// copyright of everything Uruni redistributes, which MIT, BSD, ISC, Apache-2.0
// and the OFL require to travel with the binary and the image.
//
// It is a build tool, not part of the product: its own main package, so it is
// never linked into cmd/uruni. Run it from the repo root via `make notices`.
//
// The output names each component but never a version, so a routine
// dependency bump that adds, removes or relicenses nothing leaves the file
// byte-identical and `make check` quiet.
//
//	go run ./cmd/uruni-notices -web web-notices.json -out THIRD-PARTY-NOTICES
//
// -web is the JSON web/vite.config.ts writes when URUNI_WEB_NOTICES_JSON is
// set: the npm packages that are in the built bundle and the service worker.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func main() {
	webJSON := flag.String("web", "", "JSON written by the web build (URUNI_WEB_NOTICES_JSON)")
	out := flag.String("out", "THIRD-PARTY-NOTICES", "file to write")
	flag.Parse()

	if err := run(*webJSON, *out); err != nil {
		fmt.Fprintln(os.Stderr, "uruni-notices:", err)
		os.Exit(1)
	}
}

func run(webJSON, out string) error {
	if webJSON == "" {
		return errors.New("-web is required (run `make notices`)")
	}

	fonts, err := fontComponents(".")
	if err != nil {
		return err
	}
	data, err := dataComponents(".")
	if err != nil {
		return err
	}
	goMods, err := goComponents(".")
	if err != nil {
		return err
	}
	web, err := webComponents(webJSON, ".")
	if err != nil {
		return err
	}

	doc := render([]section{
		{"FONTS", "Typefaces bundled with the app (the interface font) and the public report PDF.", fonts},
		{"DATA", "Data files embedded in the uruni binary.", data},
		{"GO", "Go modules linked into the uruni binary.", goMods},
		{"WEB", "npm packages in the built web bundle and its service worker, and the vendored shadcn/ui components.", web},
	})
	return os.WriteFile(out, []byte(doc), 0o600)
}

// fontComponents are the two OFL fonts: Plus Jakarta Sans ships inside the SPA
// bundle as woff2, Noto Sans inside the binary for the report PDF. Neither
// appears as a module, so both are listed by hand from their licence files.
func fontComponents(root string) ([]component, error) {
	var out []component
	for _, f := range []struct{ name, path string }{
		{"Plus Jakarta Sans", filepath.Join("web", "licenses", "PlusJakartaSans-OFL.txt")},
		{"Noto Sans", filepath.Join("internal", "http", "fonts", "OFL.txt")},
	} {
		text, err := readFile(filepath.Join(root, f.path))
		if err != nil {
			return nil, err
		}
		out = append(out, component{name: f.name, text: normalize(string(text))})
	}
	return out, nil
}

// dataComponents are the embedded data files that are someone else's work: the
// SecLists common-passwords list (internal/auth/commonpasswords.go), MIT.
func dataComponents(root string) ([]component, error) {
	text, err := readFile(filepath.Join(root, "internal", "auth", "SecLists-MIT.txt"))
	if err != nil {
		return nil, err
	}
	return []component{{name: "SecLists (10k-most-common password list)", text: normalize(string(text))}}, nil
}

// webComponents reads the bundle's package list and adds shadcn/ui, whose
// components are vendored source in web/src/components/ui rather than a
// package the bundler could name.
func webComponents(webJSON, root string) ([]component, error) {
	raw, err := readFile(webJSON)
	if err != nil {
		return nil, err
	}
	out, err := parseWeb(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", webJSON, err)
	}
	shadcn, err := readFile(filepath.Join(root, "web", "licenses", "shadcn-ui-MIT.txt"))
	if err != nil {
		return nil, err
	}
	return append(out, component{name: "shadcn/ui", text: normalize(string(shadcn))}), nil
}

// parseWeb decodes the web build's package list. A package with no licence text
// is an error: shipping it unattributed is the very thing this file prevents.
func parseWeb(raw []byte) ([]component, error) {
	var pkgs []struct {
		Name    string `json:"name"`
		License string `json:"license"`
	}
	if err := json.Unmarshal(raw, &pkgs); err != nil {
		return nil, err
	}
	var out []component
	var missing []string
	for _, p := range pkgs {
		text := normalize(p.License)
		if text == "" {
			missing = append(missing, p.Name)
			continue
		}
		out = append(out, component{name: p.Name, text: text})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("no licence text found for: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// goComponents lists the modules whose packages are linked into cmd/uruni and
// reads each one's licence files from the module cache.
//
// The package graph is listed for linux/amd64 with cgo off: that is the image,
// and a macOS laptop would otherwise add and drop platform-only modules
// (x/sys, go-isatty) and fail the staleness check on a tree nobody edited.
func goComponents(root string) ([]component, error) {
	cmd := exec.Command("go", "list", "-deps", "-json=ImportPath,Module", "./cmd/uruni")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w\n%s", err, stderr.String())
	}
	mods, err := parseModules(raw)
	if err != nil {
		return nil, err
	}

	var out []component
	var missing []string
	for _, m := range mods {
		text, err := moduleLicense(m.dir)
		if err != nil {
			return nil, err
		}
		if text == "" {
			missing = append(missing, m.path)
			continue
		}
		out = append(out, component{name: m.path, text: text})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("no licence file found for: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

type goModule struct{ path, dir string }

// parseModules turns `go list -deps -json` output (a stream of JSON objects)
// into the distinct third-party modules behind the listed packages. The
// standard library has no Module, and our own module is Main: neither owes a
// notice here. A replaced module is read from where it was replaced to.
func parseModules(raw []byte) ([]goModule, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	seen := map[string]goModule{}
	for dec.More() {
		var pkg struct {
			ImportPath string
			Module     *struct {
				Path    string
				Dir     string
				Main    bool
				Replace *struct{ Path, Dir string }
			}
		}
		if err := dec.Decode(&pkg); err != nil {
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}
		m := pkg.Module
		if m == nil || m.Main {
			continue
		}
		dir := m.Dir
		if m.Replace != nil && m.Replace.Dir != "" {
			dir = m.Replace.Dir
		}
		if dir == "" {
			return nil, fmt.Errorf("module %s has no directory (run `go mod download`)", m.Path)
		}
		seen[m.Path] = goModule{path: m.Path, dir: dir}
	}
	mods := make([]goModule, 0, len(seen))
	for _, m := range seen {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].path < mods[j].path })
	return mods, nil
}

// licenseNames matches the files at a module's root that carry its licence
// terms or the notices it asks to be passed on: LICENSE, LICENCE, COPYING,
// NOTICE, PATENTS (golang.org/x), with any suffix - LICENSE.md, LICENSE-SQLITE,
// LICENSE-3RD-PARTY.md. Matching is case-insensitive on the file name.
func isLicenseFile(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range []string{"LICENSE", "LICENCE", "COPYING", "NOTICE", "PATENTS"} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

// moduleLicense concatenates a module's licence files, in name order. With more
// than one (modernc.org/sqlite ships LICENSE, LICENSE-SQLITE, LICENSE-3RD-PARTY.md
// and more) each is headed by its file name so the reader can tell them apart.
func moduleLicense(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && isLicenseFile(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var texts []string
	for _, n := range names {
		b, err := readFile(filepath.Join(dir, n))
		if err != nil {
			return "", err
		}
		if t := normalize(string(b)); t != "" {
			texts = append(texts, t)
		}
	}
	if len(texts) <= 1 {
		return strings.Join(texts, ""), nil
	}
	var sb strings.Builder
	for i, t := range texts {
		if i > 0 {
			sb.WriteString("\n\n")
		}
		fmt.Fprintf(&sb, "[%s]\n\n%s", names[i], t)
	}
	return sb.String(), nil
}

type component struct {
	name string
	text string
}

type section struct {
	title string
	about string
	items []component
}

// normalize makes the same licence the same bytes wherever it was read: LF line
// ends, no trailing spaces, no blank lines around it.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

const rule = "================================================================================"

const header = `THIRD-PARTY NOTICES

Uruni is free software under the GNU Affero General Public License v3 (see
LICENSE and NOTICE). It includes the third-party components below, each under
its own licence, reproduced here as those licences require.

This file is generated - do not edit it by hand. Regenerate it with:

    make notices

It lists components by name only, with no version numbers, so a dependency
update that adds, removes or relicenses nothing leaves it unchanged. The Go
modules are the ones linked into the uruni binary; the web packages are the
ones in the built bundle and service worker, read from the build itself rather
than from the lockfile. 'make check' fails when this file is out of date.
`

// render lays the sections out. Components whose licence text is identical
// share one entry (their names listed above a single copy of the text), which
// keeps the file readable: dozens of @radix-ui packages share one MIT text.
// Everything is sorted, so the output depends only on the inputs.
func render(sections []section) string {
	var sb strings.Builder
	sb.WriteString(header)
	for _, s := range sections {
		fmt.Fprintf(&sb, "\n%s\n%s\n%s\n", rule, s.title, s.about)

		byText := map[string][]string{}
		for _, c := range s.items {
			byText[c.text] = append(byText[c.text], c.name)
		}
		type group struct {
			names []string
			text  string
		}
		groups := make([]group, 0, len(byText))
		for text, names := range byText {
			sort.Strings(names)
			groups = append(groups, group{names: names, text: text})
		}
		sort.Slice(groups, func(i, j int) bool { return groups[i].names[0] < groups[j].names[0] })

		for _, g := range groups {
			fmt.Fprintf(&sb, "\n%s\n\n%s\n\n%s\n", strings.Repeat("-", 80), strings.Join(g.names, "\n"), g.text)
		}
	}
	return sb.String()
}

// readFile is os.ReadFile for paths this tool computes itself - its own
// licence texts, the web build's JSON, the module cache - never user input.
func readFile(name string) ([]byte, error) {
	return os.ReadFile(name) //nolint:gosec // G304: a build tool reading paths it derives, not request data
}
