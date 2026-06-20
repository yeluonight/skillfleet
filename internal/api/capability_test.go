package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yeluonight/skillfleet/internal/source"
)

// The pre-refactor inline "dependency nil → 503" checks had no test
// coverage. These tests lock the capability guards' exact contract
// (return value + 503 code/message) so the byte-for-byte behaviour the
// phase8.5-t3 extraction preserved cannot silently drift later.

// decode503 reads a recorder it expects to hold a 503 and returns the
// JSON error envelope.
func decode503(t *testing.T, rec *httptest.ResponseRecorder) apiError {
	t.Helper()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body apiError
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func TestCapabilityGuards_Absent503(t *testing.T) {
	// Every guard runs against a zero-value Deps (all optional fields
	// nil), so each must take its absence branch.
	var d Deps
	cases := []struct {
		name    string
		call    func(http.ResponseWriter) bool
		code    string
		message string
	}{
		{"registry", d.requireRegistry, "registry_unavailable", "registry not configured"},
		{"drafts", d.requireDrafts, "drafts_unavailable", "drafts not configured"},
		{"deploy", d.requireDeploy, "deploy_unavailable", "deployment not configured"},
		{"sources_no_fetcher", func(w http.ResponseWriter) bool { return d.requireSources(w, false) },
			"sources_unavailable", "source binding not configured"},
		{"sources_need_fetcher", func(w http.ResponseWriter) bool { return d.requireSources(w, true) },
			"sources_unavailable", "source binding not configured"},
		{"registry_and_sources", d.requireRegistryAndSources, "sources_unavailable", "source binding not configured"},
		{"deploy_stack", d.requireDeployStack, "deploy_unavailable", "deployment not configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if tc.call(rec) {
				t.Fatal("guard returned true for nil dependency, want false")
			}
			body := decode503(t, rec)
			if body.Error != tc.code {
				t.Errorf("code = %q, want %q", body.Error, tc.code)
			}
			if body.Message != tc.message {
				t.Errorf("message = %q, want %q", body.Message, tc.message)
			}
		})
	}
}

// requireSources(w, true) must also reject when only the Fetcher is
// missing (Sources present) — the bind/preview/check handlers need both.
func TestRequireSources_FetcherOnlyMissing(t *testing.T) {
	// Sources present (non-nil pointer; guards only nil-check it), Fetcher
	// absent. A zero-value Store suffices — no method is called.
	d := Deps{Sources: &source.Store{}, Fetcher: nil}
	rec := httptest.NewRecorder()
	if d.requireSources(rec, true) {
		t.Fatal("requireSources(true) returned true with nil Fetcher, want false")
	}
	body := decode503(t, rec)
	if body.Error != "sources_unavailable" {
		t.Errorf("code = %q, want sources_unavailable", body.Error)
	}
	// With needFetcher=false the same Deps must pass: detach only needs
	// the store.
	rec2 := httptest.NewRecorder()
	if !d.requireSources(rec2, false) {
		t.Error("requireSources(false) returned false with Sources present, want true")
	}
}
