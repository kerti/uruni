package http

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/ledger"
)

// TestErrorCodesMatchTheWebList keeps web/src/test/wire/error-codes.json equal to
// every code this package can send: the ledger's own (each ledger.Error names
// its code), and those read from this package's source, the first string
// argument after the status of each writeAPIError and writeAPIFieldError call.
// The web side asserts each one has copy (#524), so a new code cannot reach the
// treasurer as the generic "Ada yang tidak beres".
//
// A code passed as anything but a string literal fails here rather than slip
// past the list.
func TestErrorCodesMatchTheWebList(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	codes := map[string]bool{}
	for _, e := range ledger.Errors() {
		codes[e.Code()] = true
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || (fn.Name != "writeAPIError" && fn.Name != "writeAPIFieldError") || len(call.Args) < 3 {
				return true
			}
			// writeAPIError's own body forwards its parameter, and mapLedgerError sends
			// a ledger.Error's Code(); neither is a literal, and the second is listed
			// above from ledger.Errors().
			if name == "errors.go" {
				if _, inner := call.Args[2].(*ast.Ident); inner {
					return true
				}
				if c, ok := call.Args[2].(*ast.CallExpr); ok {
					if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Code" {
						return true
					}
				}
			}
			lit, ok := call.Args[2].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				t.Errorf("%s: %s sends a code that is not a string literal; the web list cannot see it", fset.Position(call.Pos()), fn.Name)
				return true
			}
			code, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("%s: %v", fset.Position(lit.Pos()), err)
			}
			codes[code] = true
			return true
		})
	}

	list := make([]string, 0, len(codes))
	for code := range codes {
		list = append(list, code)
	}
	slices.Sort(list)
	got, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, filepath.Join(wireDir, "error-codes.json"), append(got, '\n'))
}
