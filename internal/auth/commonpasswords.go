package auth

import (
	_ "embed"
	"strings"
	"sync"
)

// commonPasswordsTXT is the list of common passwords (#528) a new password is
// refused against, one lower-case entry per line.
//
// Provenance: SecLists, Passwords/Common-Credentials/10k-most-common.txt
// (https://github.com/danielmiessler/SecLists, MIT licence, Copyright (c) 2018
// Daniel Miessler; the notice is in THIRD-PARTY-NOTICES). Retrieved 2026-10-20
// from https://raw.githubusercontent.com/danielmiessler/SecLists/master/Passwords/Common-Credentials/10k-most-common.txt
// (file last changed upstream in commit 913b327, 2026-09-08): 10001 lines,
// plain ASCII. Filtered here to entries of at least MinPasswordLength bytes
// (shorter ones are already refused as too short), lower-cased and de-duplicated,
// which leaves 2087.
//
//go:embed commonpasswords.txt
var commonPasswordsTXT string

var (
	commonPasswordsOnce sync.Once
	commonPasswords     map[string]struct{}
)

// isCommonPassword reports whether password is on the list, ignoring case. The
// password is matched as entered: no trimming, as hashing takes it as entered.
func isCommonPassword(password string) bool {
	commonPasswordsOnce.Do(func() {
		lines := strings.Split(commonPasswordsTXT, "\n")
		commonPasswords = make(map[string]struct{}, len(lines))
		for _, l := range lines {
			if l = strings.TrimSpace(l); l != "" {
				commonPasswords[l] = struct{}{}
			}
		}
	})
	_, ok := commonPasswords[strings.ToLower(password)]
	return ok
}
