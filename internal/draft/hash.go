package draft

import (
	"crypto/sha256"
	"encoding/hex"
)

// sha256Hex returns the lowercase hex sha256 of content. Used to name
// binary draft blobs and to record a per-file digest.
func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
