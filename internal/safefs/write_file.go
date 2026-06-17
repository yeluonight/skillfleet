package safefs

import (
	"fmt"
	"os"
)

// WriteFile is the project-wide filesystem write chokepoint for simple
// whole-file writes outside skill package trees. It creates or truncates
// path using os.WriteFile while giving callers one audited import to use
// instead of writing files directly.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	if path == "" {
		return fmt.Errorf("safefs: empty file path")
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("safefs: write %s: %w", path, err)
	}
	return nil
}
