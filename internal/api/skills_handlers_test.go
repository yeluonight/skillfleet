package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/draft"
	"github.com/yeluonight/skillfleet/internal/ratelimit"
	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/migrations"
)

// newTestServerWithRegistry builds a server whose Deps include a
// registry.Store rooted at a temp dir, for the /api/skills* routes.
func newTestServerWithRegistry(t *testing.T) (*httptest.Server, *sql.DB, *registry.Store) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "skills.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(t.TempDir(), "store")
	reg, err := registry.New(d, storeDir)
	if err != nil {
		t.Fatal(err)
	}
	// Drafts share the registry's store root so a forked version's
	// archive is reachable.
	drafts, err := draft.New(d, reg, storeDir)
	if err != nil {
		t.Fatal(err)
	}
	rate := ratelimit.Rate{Limit: 100, Window: time.Minute}
	srv := httptest.NewServer(NewRouter(Deps{
		DB:         d,
		Now:        time.Now,
		Audit:      audit.New(d, nil, time.Now),
		SessionTTL: time.Hour,
		LoginIP:    rate,
		LoginUser:  rate,
		Registry:   reg,
		Drafts:     drafts,
	}))
	t.Cleanup(func() {
		srv.Close()
		_ = d.Close()
	})
	return srv, d, reg
}

func TestCreateSkill_CreatesInitialVersion(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	body := map[string]string{"name": "deploy-helper", "description": "deploys things"}
	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills", body)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got skillDetailView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "deploy-helper" {
		t.Errorf("name = %q", got.Name)
	}
	if len(got.Versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(got.Versions))
	}
	if got.Versions[0].Kind != "manual" || got.Versions[0].FileCount != 1 {
		t.Errorf("initial version = %+v", got.Versions[0])
	}
}

func TestCreateSkill_DuplicateConflicts(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	mk := func() *http.Response {
		req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills", map[string]string{"name": "dup"})
		return authedDo(t, sc, cc, req)
	}
	r1 := mk()
	r1.Body.Close()
	if r1.StatusCode != http.StatusCreated {
		t.Fatalf("first create = %d", r1.StatusCode)
	}
	r2 := mk()
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusConflict {
		t.Errorf("duplicate create = %d, want 409", r2.StatusCode)
	}
}

func TestCreateSkill_RejectsBadName(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	for _, bad := range []string{"", "has space", "a/b", "..", "with\ttab"} {
		req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills", map[string]string{"name": bad})
		resp := authedDo(t, sc, cc, req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("name %q = %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestListSkills_ReturnsCreated(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	for _, n := range []string{"alpha", "beta"} {
		req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills", map[string]string{"name": n})
		authedDo(t, sc, cc, req).Body.Close()
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got struct {
		Skills []skillSummaryView `json:"skills"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 2 {
		t.Fatalf("skills = %d, want 2", len(got.Skills))
	}
	for _, s := range got.Skills {
		if s.VersionCount != 1 || s.LatestVersionID == "" {
			t.Errorf("summary = %+v", s)
		}
	}
}

func TestGetSkill_ReturnsVersions(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req := newJSONReq(t, http.MethodPost, srv.URL+"/api/skills", map[string]string{"name": "solo"})
	authedDo(t, sc, cc, req).Body.Close()

	greq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/solo", nil)
	resp := authedDo(t, sc, cc, greq)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got skillDetailView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "solo" || len(got.Versions) != 1 {
		t.Errorf("detail = %+v", got)
	}
}

func TestGetSkill_UnknownReturns404(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skills/ghost", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSkills_RequireAuth(t *testing.T) {
	srv, _, _ := newTestServerWithRegistry(t)
	resp, err := http.Get(srv.URL + "/api/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// newJSONReq builds a JSON POST/PUT request with the right Content-Type.
func newJSONReq(t *testing.T, method, url string, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}
