package http

import "testing"

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
