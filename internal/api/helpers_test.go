package api

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/setup"
)

// ensureSetupCode primes the setup_state row and returns the plaintext
// code so the test can POST /api/setup with it.
func ensureSetupCode(t *testing.T, d *sql.DB) (string, bool, error) {
	t.Helper()
	return setup.EnsureCode(context.Background(), d, time.Now())
}
