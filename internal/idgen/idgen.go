// Package idgen mints application-layer identifiers.
//
// Format: "<prefix>_<26-char-lower-base32>". The 26-char tail is 16
// random bytes encoded with the unpadded RFC 4648 base32 alphabet, so
// it is URL/log/SQL safe and a fixed width across all SkillFleet
// tables. The prefix marks the row type at a glance (`usr_…`,
// `ses_…`, `tok_…`, `log_…`). No timestamp is embedded — ordering is
// driven by `created_at` columns, not by the id.
package idgen

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
)

// New returns an id of the form "<prefix>_<26-lower-base32-chars>".
// prefix must be 2–8 lowercase ascii letters; the returned string is
// 26+len(prefix)+1 characters long.
//
// New panics only if crypto/rand fails, which on Linux means the
// kernel CSPRNG itself is unavailable — non-recoverable.
func New(prefix string) string {
	if !validPrefix(prefix) {
		panic(fmt.Sprintf("idgen: bad prefix %q (need 2-8 lower-ascii letters)", prefix))
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("idgen: rand.Read: %v", err))
	}
	// base32 stdencoding with padding stripped: 16 bytes → 26 chars.
	enc := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]))
	return prefix + "_" + enc
}

func validPrefix(p string) bool {
	if len(p) < 2 || len(p) > 8 {
		return false
	}
	for _, c := range p {
		if c < 'a' || c > 'z' {
			return false
		}
	}
	return true
}
