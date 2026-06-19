// Package auth holds password hashing + verification primitives.
//
// Parameters are locked to IMPLEMENTATION_PLAN.md §5.2: argon2id with
// m=64MB, t=3, p=2, 16-byte salt, 32-byte derived key. Parameters and
// salt travel with the hash so future tuning doesn't lock out existing
// accounts.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	Argon2Memory     uint32 = 64 * 1024 // KiB
	Argon2Iterations uint32 = 3
	Argon2Parallel   uint8  = 2
	Argon2SaltLen    uint32 = 16
	Argon2KeyLen     uint32 = 32
)

// ErrPasswordMismatch is returned by VerifyPassword when the password
// does not match the stored hash. Callers should treat this as
// indistinguishable from "user not found" to avoid enumeration.
var ErrPasswordMismatch = errors.New("auth: password mismatch")

// HashPassword returns an argon2id-encoded hash of pw using the
// project's locked parameters. The encoded form is self-describing,
// matching the libsodium / PHC layout:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt-b64>$<hash-b64>
func HashPassword(pw string) (string, error) {
	if pw == "" {
		return "", errors.New("auth: empty password")
	}
	salt := make([]byte, Argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, Argon2Iterations, Argon2Memory, Argon2Parallel, Argon2KeyLen)
	return encode(salt, key, Argon2Memory, Argon2Iterations, Argon2Parallel), nil
}

// VerifyPassword returns nil iff pw produces the same derived key as
// encoded under the parameters embedded in encoded.
//
// On any malformed input, it returns ErrPasswordMismatch rather than a
// distinct parse error — the caller is presumed untrusted (login
// path), and a parse error would leak whether the user record exists.
func VerifyPassword(encoded, pw string) error {
	mem, iter, par, salt, key, err := decode(encoded)
	if err != nil {
		return ErrPasswordMismatch
	}
	candidate := argon2.IDKey([]byte(pw), salt, iter, mem, par, uint32(len(key)))
	if subtle.ConstantTimeCompare(candidate, key) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

func encode(salt, key []byte, mem, iter uint32, par uint8) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, mem, iter, par,
		b64.EncodeToString(salt), b64.EncodeToString(key),
	)
}

func decode(encoded string) (mem, iter uint32, par uint8, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, errors.New("auth: bad format")
	}
	var ver int
	if _, err = fmt.Sscanf(parts[2], "v=%d", &ver); err != nil || ver != argon2.Version {
		return 0, 0, 0, nil, nil, errors.New("auth: bad version")
	}
	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &iter, &par); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("auth: bad params: %w", err)
	}
	b64 := base64.RawStdEncoding
	if salt, err = b64.DecodeString(parts[4]); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("auth: bad salt: %w", err)
	}
	if key, err = b64.DecodeString(parts[5]); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("auth: bad key: %w", err)
	}
	return mem, iter, par, salt, key, nil
}
