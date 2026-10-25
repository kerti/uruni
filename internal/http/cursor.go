package http

import (
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

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
