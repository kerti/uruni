// Package stack renders a recovered panic for a log line without leaking data.
// Both the backup scheduler and the HTTP recoverer log panics, and both owe
// ADR-022 the same care, so the care lives in one place.
package stack

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// WithoutArgs keeps a goroutine stack's function names and file:line
// positions and drops each frame's argument words. debug.Stack prints those as
// raw hex, and an int64 argument is an amount (ADR-022).
func WithoutArgs(stack []byte) string {
	lines := strings.Split(string(stack), "\n")
	for i, line := range lines {
		if line == "" || strings.HasPrefix(line, "\t") || !strings.HasSuffix(line, ")") {
			continue
		}
		if open := strings.LastIndex(line, "("); open > 0 {
			lines[i] = line[:open] + "(...)"
		}
	}
	return strings.Join(lines, "\n")
}

// Describe is what to log for a recovered panic value: the runtime's own
// message for a runtime.Error (an index out of range, a nil dereference: fixed
// text, no data), and only the value's type for anything else, since a
// panic(fmt.Sprintf(...)) or a wrapped error can carry a name or an amount.
func Describe(v any) string {
	what := fmt.Sprintf("%T", v)
	var rerr runtime.Error
	if err, ok := v.(error); ok && errors.As(err, &rerr) {
		what = rerr.Error()
	}
	return what
}
