// Package safefs is the single chokepoint for every filesystem path
// that crosses the boundary between untrusted input (uploaded zips,
// WebUI file-tree operations, draft file paths) and real disk writes.
//
// v1.0 §1.3.1 makes this non-negotiable: no skill content may be
// written to disk except through safefs. This first file implements
// the path guard — the validator that turns an arbitrary caller-
// supplied string into a known-safe, package-relative path, or
// rejects it. Later files in this package (staging / backup /
// atomic_replace / manifest_delete, Phase 8) build on the guarantee
// established here: a path that survives CleanPackagePath cannot
// escape the package root.
//
// "Package-relative" means a forward-slash path naming a file inside a
// skill package, e.g. "SKILL.md" or "scripts/deploy.py". It is never
// absolute, never contains "..", never names a Windows drive, and
// never carries control characters or backslashes. The canonical form
// uses "/" exclusively (v1.0 §7.2).
package safefs

import (
	"errors"
	"path"
	"strings"
)

// Path-guard limits. These bound a single package-internal path so a
// hostile zip entry can't exhaust memory or smuggle a pathological
// nesting depth past the validator.
const (
	// MaxPathBytes caps the length of one package-relative path. 1024
	// is generous for real skill trees and well under any filesystem
	// component/length limit once joined to a root.
	MaxPathBytes = 1024
	// MaxPathDepth caps the number of "/"-separated components. Deeply
	// nested trees are almost always an attack or a packaging bug.
	MaxPathDepth = 32
)

// Errors returned by CleanPackagePath. Callers branch on these via
// errors.Is to map to the right HTTP status (all of them are client
// errors → 400) while logging the precise reason server-side.
var (
	ErrEmptyPath     = errors.New("safefs: empty path")
	ErrAbsolutePath  = errors.New("safefs: absolute path not allowed")
	ErrDriveLetter   = errors.New("safefs: windows drive path not allowed")
	ErrDotDot        = errors.New("safefs: parent-directory segment not allowed")
	ErrBackslash     = errors.New("safefs: backslash not allowed; use '/'")
	ErrControlChar   = errors.New("safefs: control character in path")
	ErrTrailingSlash = errors.New("safefs: path must name a file, not a directory")
	ErrPathTooLong   = errors.New("safefs: path exceeds length limit")
	ErrPathTooDeep   = errors.New("safefs: path exceeds depth limit")
	ErrReservedName  = errors.New("safefs: reserved path segment")
)

// CleanPackagePath validates raw as a package-relative file path and
// returns its canonical form. The returned path:
//
//   - uses "/" as the only separator,
//   - has no leading "/", no "." or ".." segment, no "" segment,
//   - is the lexical clean of the input (collapsed "//", resolved "."),
//   - names a file (no trailing slash).
//
// It rejects, rather than sanitises, anything ambiguous: a path that
// "looks like" an escape attempt is an error, never silently rewritten
// into something safe. This keeps the audit story honest — what the
// caller asked for is what we judged.
//
// The validation runs BEFORE path.Clean so that escapes are detected
// on the raw input. We do not want path.Clean to quietly fold
// "a/../../b" down to "../b" and have us inspect only the result; we
// reject the "../" the moment we see it.
func CleanPackagePath(raw string) (string, error) {
	if raw == "" {
		return "", ErrEmptyPath
	}
	if len(raw) > MaxPathBytes {
		return "", ErrPathTooLong
	}

	// Backslashes are rejected outright rather than translated: a
	// caller sending "scripts\deploy.py" is either on the wrong OS
	// convention or probing, and translating would mask the latter.
	if strings.ContainsRune(raw, '\\') {
		return "", ErrBackslash
	}

	// Control characters (including NUL, newline, tab, DEL) never
	// belong in a package path and several break shells / archives /
	// the fingerprint line format. Reject the whole C0 range + DEL.
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", ErrControlChar
		}
	}

	// Windows drive escapes: "C:", "C:/x", and the rooted form. We
	// check before stripping so "C:\..." (already caught by the
	// backslash rule) and "C:/..." both fail clearly.
	if hasDriveLetter(raw) {
		return "", ErrDriveLetter
	}

	// Absolute paths (POSIX). A leading "/" means "from the root",
	// which is exactly the escape we forbid inside a package.
	if strings.HasPrefix(raw, "/") {
		return "", ErrAbsolutePath
	}

	// A trailing slash means the caller named a directory; package
	// paths always name a file. (We also reject it because path.Clean
	// would strip it, hiding the caller's intent.)
	if strings.HasSuffix(raw, "/") {
		return "", ErrTrailingSlash
	}

	// Inspect raw segments for ".." and "" before normalising, so the
	// escape is caught on the literal input.
	rawSegs := strings.Split(raw, "/")
	for _, seg := range rawSegs {
		switch seg {
		case "":
			// Empty segment from "//" or a leading/trailing slash. A
			// leading "/" is already caught above; an interior "//"
			// is a malformed path we refuse rather than collapse.
			return "", ErrEmptyPath
		case ".":
			// A bare "." segment is redundant; refuse so the canonical
			// form is unique (no "a/./b" vs "a/b" ambiguity).
			return "", ErrReservedName
		case "..":
			return "", ErrDotDot
		}
	}

	// Lexical clean is now a no-op on structure (no "."/".." left) but
	// still collapses nothing dangerous; we run it to get the
	// canonical string and then re-assert the invariants defensively.
	cleaned := path.Clean(raw)

	// path.Clean can only have changed things if our segment scan
	// missed a case; treat any post-clean escape as a hard error.
	if cleaned == "." || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", ErrDotDot
	}

	depth := strings.Count(cleaned, "/") + 1
	if depth > MaxPathDepth {
		return "", ErrPathTooDeep
	}

	return cleaned, nil
}

// hasDriveLetter reports whether s begins with a Windows drive prefix
// like "C:" or "c:/". Matching is restricted to a single ASCII letter
// followed by a colon, which is the only shape that grants drive-root
// semantics; "ab:cd" (a colon mid-segment) is left to other rules.
func hasDriveLetter(s string) bool {
	if len(s) < 2 {
		return false
	}
	c := s[0]
	isLetter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
	return isLetter && s[1] == ':'
}

// IsCleanPackagePath reports whether raw is already in canonical
// package-relative form (i.e. CleanPackagePath returns it unchanged
// with no error). Useful as a cheap assertion at trust boundaries
// where the path is expected to have been cleaned upstream.
func IsCleanPackagePath(raw string) bool {
	cleaned, err := CleanPackagePath(raw)
	return err == nil && cleaned == raw
}
