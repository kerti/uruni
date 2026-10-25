package ledger

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryDomainErrorCarriesItsWireAnswer: the HTTP layer answers a ledger.Error
// from what it carries and nothing else, so a blank code, message or class would
// reach a client as a blank envelope.
func TestEveryDomainErrorCarriesItsWireAnswer(t *testing.T) {
	t.Parallel()
	errs := Errors()
	if len(errs) == 0 {
		t.Fatal("Errors() = none, want every domain error")
	}
	for _, e := range errs {
		switch e.Class() {
		case ClassInvalid, ClassNotFound, ClassConflict:
		default:
			t.Errorf("%q: class = %d, want a defined Class", e.Error(), e.Class())
		}
		if e.Code() == "" || strings.ToLower(e.Code()) != e.Code() || strings.ContainsAny(e.Code(), " -") {
			t.Errorf("%q: code = %q, want a non-empty snake_case slug", e.Error(), e.Code())
		}
		if e.Message() == "" {
			t.Errorf("%q: message is empty", e.Error())
		}
		if !strings.HasPrefix(e.Error(), "ledger: ") {
			t.Errorf("%q: text does not start with \"ledger: \"", e.Error())
		}
	}
}

// TestDomainErrorsShareACodeOnlyWhereTheyAlwaysHave: one error, one code (ADR-033),
// except these, where the client's answer is the same for different refusals.
// A code shared by new errors fails here until it is listed on purpose; the same
// code always means the same Class.
func TestDomainErrorsShareACodeOnlyWhereTheyAlwaysHave(t *testing.T) {
	t.Parallel()
	allowed := map[string]int{
		// A reversal's or a correction's id that names no row of this fund.
		"not_found": 2,
		// The generic shape refusal, and the three refusals that point at a field
		// without a code of their own: a purpose, an account, a named contribution's
		// purpose.
		"invalid_argument": 4,
		// A second rate for a month the tier already has, answered as the schema's
		// own UNIQUE would be.
		"unique_violation": 1,
	}
	count := map[string]int{}
	class := map[string]Class{}
	for _, e := range Errors() {
		count[e.Code()]++
		if c, ok := class[e.Code()]; ok && c != e.Class() {
			t.Errorf("code %q is used with two classes (%d and %d)", e.Code(), c, e.Class())
		}
		class[e.Code()] = e.Class()
	}
	for code, n := range count {
		if want := max(allowed[code], 1); n != want {
			t.Errorf("code %q is carried by %d errors, want %d", code, n, want)
		}
	}
}

// TestNoSentinelBypassesRefuse: an Err* variable made with errors.New is not a
// ledger.Error, so the HTTP layer would answer it 500 with no one noticing. This
// reads the package source for one. It sees package-level vars named Err* only:
// an unexported sentinel, a var declared without a value, or a refuse call
// inside a function escape it.
func TestNoSentinelBypassesRefuse(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, id := range vs.Names {
					if !strings.HasPrefix(id.Name, "Err") || i >= len(vs.Values) {
						continue
					}
					call, ok := vs.Values[i].(*ast.CallExpr)
					if !ok {
						t.Errorf("%s: %s is not defined by refuse(...)", fset.Position(id.Pos()), id.Name)
						continue
					}
					if fn, ok := call.Fun.(*ast.Ident); !ok || fn.Name != "refuse" {
						t.Errorf("%s: %s is not defined by refuse(...), so it carries no wire code", fset.Position(id.Pos()), id.Name)
					}
				}
			}
		}
	}
}
