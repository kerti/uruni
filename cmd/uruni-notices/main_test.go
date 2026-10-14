package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseModulesKeepsOnlyThirdPartyOnce(t *testing.T) {
	raw := []byte(`
{"ImportPath":"fmt"}
{"ImportPath":"github.com/kerti/uruni/internal/money","Module":{"Path":"github.com/kerti/uruni","Dir":"/src","Main":true}}
{"ImportPath":"github.com/go-chi/chi/v5","Module":{"Path":"github.com/go-chi/chi/v5","Dir":"/mod/chi@v5.3.2"}}
{"ImportPath":"github.com/go-chi/chi/v5/middleware","Module":{"Path":"github.com/go-chi/chi/v5","Dir":"/mod/chi@v5.3.2"}}
{"ImportPath":"example.com/a","Module":{"Path":"example.com/a","Dir":"/mod/a@v1","Replace":{"Path":"../a","Dir":"/local/a"}}}
`)
	mods, err := parseModules(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, m := range mods {
		got = append(got, m.path+"="+m.dir)
	}
	want := "example.com/a=/local/a,github.com/go-chi/chi/v5=/mod/chi@v5.3.2"
	if strings.Join(got, ",") != want {
		t.Errorf("modules = %v, want %s", got, want)
	}
}

func TestParseModulesNeedsADirectory(t *testing.T) {
	_, err := parseModules([]byte(`{"ImportPath":"x.org/y","Module":{"Path":"x.org/y"}}`))
	if err == nil {
		t.Fatal("a module with no directory must be an error, not a silent omission")
	}
}

func TestModuleLicenseFindsLicenceFilesOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("LICENSE", "MIT text  \r\n")
	write("NOTICE.txt", "Copyright Someone")
	write("PATENTS", "patent grant")
	write("README.md", "not a licence")
	write("main.go", "package x")
	write("licence-extra", "lowercase, other spelling")

	got, err := moduleLicense(dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "not a licence") || strings.Contains(got, "package x") {
		t.Errorf("picked up a non-licence file:\n%s", got)
	}
	for _, want := range []string{"[LICENSE]\n\nMIT text\n", "[NOTICE.txt]\n\nCopyright Someone", "[PATENTS]", "[licence-extra]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "[LICENSE]") > strings.Index(got, "[NOTICE.txt]") {
		t.Errorf("files are not in name order:\n%s", got)
	}
}

func TestModuleLicenseSingleFileHasNoHeading(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("\nBSD\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := moduleLicense(dir)
	if err != nil || got != "BSD" {
		t.Errorf("got %q, %v; want %q", got, err, "BSD")
	}
}

func TestParseWebRefusesUnlicensedPackage(t *testing.T) {
	_, err := parseWeb([]byte(`[{"name":"ok","license":"MIT"},{"name":"bare","license":"  "}]`))
	if err == nil || !strings.Contains(err.Error(), "bare") {
		t.Errorf("err = %v, want one naming the unlicensed package", err)
	}
}

func TestRenderIsSortedGroupedAndVersionLess(t *testing.T) {
	items := []component{
		{name: "zeta", text: "MIT A"},
		{name: "alpha", text: "BSD"},
		{name: "beta", text: "MIT A"},
	}
	a := render([]section{{"GO", "about", items}})
	reversed := []component{items[2], items[1], items[0]}
	b := render([]section{{"GO", "about", reversed}})
	if a != b {
		t.Error("output depends on input order")
	}
	// beta and zeta share one text, listed together once.
	if strings.Count(a, "MIT A") != 1 || !strings.Contains(a, "beta\nzeta\n\nMIT A") {
		t.Errorf("identical texts not grouped:\n%s", a)
	}
	if strings.Index(a, "alpha") > strings.Index(a, "beta") {
		t.Errorf("groups not sorted by first name:\n%s", a)
	}
}
