package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The cursor bytes are a wire format a client holds across a deploy: these
// strings must never change.
func TestCursorEncodingIsPinned(t *testing.T) {
	cases := []struct{ kind, got, want string }{
		{"transactions", encodeCursor("2026-03-15", 42), "MjAyNi0wMy0xNXw0Mg"},
		{"reimbursements", encodeCursor("2026-03-15", 42), "MjAyNi0wMy0xNXw0Mg"},
		{"dues payments", encodeCursor("2026-03-15", 42), "MjAyNi0wMy0xNXw0Mg"},
		{"members", encodeCursor("Siti Aminah", 7), "U2l0aSBBbWluYWh8Nw"},
		{"reconciliations", encodeCursor("1700000000", 9), "MTcwMDAwMDAwMHw5"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s cursor = %q, want %q", c.kind, c.got, c.want)
		}
	}
}

func TestMembersCursorKeepsAPipeInTheName(t *testing.T) {
	name, id, ok := decodeNameCursor(encodeCursor("Ani | Budi", 12))
	if !ok || name != "Ani | Budi" || id != 12 {
		t.Fatalf("decodeNameCursor = %q, %d, %v", name, id, ok)
	}
}

func TestDecodeCursorRejectsMalformedInput(t *testing.T) {
	for _, raw := range []string{"!!!", encodeCursor("x", 0), encodeCursor("x", -3), "bm9zZXA"} {
		if _, _, ok := decodeCursor(raw); ok {
			t.Errorf("decodeCursor(%q) accepted", raw)
		}
	}
	if _, _, ok := decodeDateCursor(encodeCursor("2026-02-30", 1)); ok {
		t.Error("decodeDateCursor accepted an impossible date")
	}
	if _, _, ok := decodeNameCursor(encodeCursor("", 1)); ok {
		t.Error("decodeNameCursor accepted an empty name")
	}
	if _, _, ok := decodeInt64Cursor(encodeCursor("0", 1)); ok {
		t.Error("decodeInt64Cursor accepted 0")
	}
}

func TestPageLimitPeeksOneExtraRow(t *testing.T) {
	if pageLimit != pageSize+1 {
		t.Fatalf("pageLimit = %d, want pageSize+1 = %d", pageLimit, pageSize+1)
	}
}

func TestTrimPage(t *testing.T) {
	keyOf := func(n int) (string, int64) { return "k", int64(n) }
	rows := func(n int) []int {
		out := make([]int, n)
		for i := range out {
			out[i] = i + 1
		}
		return out
	}
	cases := []struct {
		name     string
		in       int
		wantRows int
		wantNext bool
	}{
		{"fewer than a page", pageSize - 1, pageSize - 1, false},
		{"exactly a page is the last page", pageSize, pageSize, false},
		{"one past a page trims and points on", pageSize + 1, pageSize, true},
		{"empty", 0, 0, false},
	}
	for _, c := range cases {
		got, next := trimPage(rows(c.in), keyOf)
		if len(got) != c.wantRows || (next != nil) != c.wantNext {
			t.Errorf("%s: rows=%d next=%v, want rows=%d next=%v", c.name, len(got), next != nil, c.wantRows, c.wantNext)
			continue
		}
		if c.wantNext {
			if want := encodeCursor("k", int64(pageSize)); *next != want {
				t.Errorf("%s: next_cursor = %q, want the last kept row's %q", c.name, *next, want)
			}
		}
	}
}

func TestReadCursor(t *testing.T) {
	// No cursor: first page, zero key, nil id.
	w := httptest.NewRecorder()
	key, id, ok := readCursor(w, httptest.NewRequest(http.MethodGet, "/x", nil), decodeNameCursor)
	if !ok || key != "" || id != nil {
		t.Fatalf("no cursor = %q, %v, %v", key, id, ok)
	}

	// A good cursor yields its key and id.
	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x?cursor="+encodeCursor("Siti", 7), nil)
	key, id, ok = readCursor(w, r, decodeNameCursor)
	if !ok || key != "Siti" || id == nil || *id != 7 {
		t.Fatalf("good cursor = %q, %v, %v", key, id, ok)
	}

	// A bad cursor writes the 400 on field cursor.
	w = httptest.NewRecorder()
	_, _, ok = readCursor(w, httptest.NewRequest(http.MethodGet, "/x?cursor=!!!", nil), decodeNameCursor)
	body := w.Body.String()
	if ok || w.Code != http.StatusBadRequest || !strings.Contains(body, `"invalid_argument"`) || !strings.Contains(body, `"field":"cursor"`) {
		t.Fatalf("bad cursor: ok=%v code=%d body=%s", ok, w.Code, body)
	}
}
