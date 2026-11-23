package http

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// pageSize is every keyset-paged list's page length: one number, no per-list
// override. A list asks its query for pageLimit rows (one extra, the peek that
// says whether a next page exists) and hands the rows to trimPage.
const (
	pageSize  = 25
	pageLimit = pageSize + 1
)

// readCursor decodes ?cursor= with the list's key-type decoder. No cursor
// answers the zero key and a nil id (the first page): a caller sets its
// params' nullable cursor key only when the id is non-nil. A bad cursor writes
// the 400 invalid_argument on field "cursor" and answers ok=false: the caller
// returns.
func readCursor[K any](w http.ResponseWriter, r *http.Request, decode func(string) (K, int64, bool)) (key K, id *int64, ok bool) {
	raw := r.URL.Query().Get("cursor")
	if raw == "" {
		return key, nil, true
	}
	key, n, valid := decode(raw)
	if !valid {
		writeAPIFieldError(w, http.StatusBadRequest, "invalid_argument", "cursor", "The cursor is not valid.")
		return key, nil, false
	}
	return key, &n, true
}

// trimPage cuts the peeked extra row and, when there was one, encodes the last
// kept row's (key, id) as next_cursor; nil on the last page. keyOf names the
// list's keyset pair.
func trimPage[R any](rows []R, keyOf func(R) (string, int64)) ([]R, *string) {
	if len(rows) <= pageSize {
		return rows, nil
	}
	rows = rows[:pageSize]
	encoded := encodeCursor(keyOf(rows[len(rows)-1]))
	return rows, &encoded
}

// Every keyset-paged list hands the client an opaque cursor: base64url (no
// padding) of "key|id", the pair its keyset WHERE compares. The id is a
// positive integer and never contains "|", so decoding splits on the last one
// and a key (a member name) may itself hold a "|". The bytes are a wire format
// a client keeps across a deploy.
func encodeCursor(key string, id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key + "|" + strconv.FormatInt(id, 10)))
}

// decodeCursor rejects bad base64, a missing separator and an id that is not a
// positive integer. The key's own shape is the caller's check (the decode*Cursor
// helpers below).
func decodeCursor(raw string) (key string, id int64, ok bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", 0, false
	}
	key, idPart, found := cutLast(string(decoded), "|")
	if !found {
		return "", 0, false
	}
	id, err = strconv.ParseInt(idPart, 10, 64)
	if err != nil || id <= 0 {
		return "", 0, false
	}
	return key, id, true
}

func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// decodeDateCursor additionally requires the key to round-trip to a real
// calendar date (occurredOnLayout).
func decodeDateCursor(raw string) (date string, id int64, ok bool) {
	date, id, ok = decodeCursor(raw)
	if !ok {
		return "", 0, false
	}
	t, err := time.Parse(occurredOnLayout, date)
	if err != nil || t.Format(occurredOnLayout) != date {
		return "", 0, false
	}
	return date, id, true
}

// decodeNameCursor additionally requires a non-empty key.
func decodeNameCursor(raw string) (name string, id int64, ok bool) {
	name, id, ok = decodeCursor(raw)
	if !ok || name == "" {
		return "", 0, false
	}
	return name, id, true
}

// decodeInt64Cursor additionally requires the key to be a positive integer
// (a Unix timestamp).
func decodeInt64Cursor(raw string) (key, id int64, ok bool) {
	part, id, ok := decodeCursor(raw)
	if !ok {
		return 0, 0, false
	}
	key, err := strconv.ParseInt(part, 10, 64)
	if err != nil || key <= 0 {
		return 0, 0, false
	}
	return key, id, true
}
