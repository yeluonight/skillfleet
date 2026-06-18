package enrollclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/agentcfg"
)

// fakeServer mounts a handler at /agent/enroll that captures the
// inbound request and returns whatever status / body the test wants.
type fakeServer struct {
	srv         *httptest.Server
	gotBody     map[string]any
	gotHeaders  http.Header
	respStatus  int
	respBody    any
	hits        int
}

func newFake(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{respStatus: http.StatusCreated, respBody: map[string]any{
		"device_id":     "dev_abc",
		"device_secret": "s3cret",
		"status":        "pending",
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("/agent/enroll", func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		f.gotHeaders = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&f.gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.respStatus)
		_ = json.NewEncoder(w).Encode(f.respBody)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestRun_HappyPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.json")
	f := newFake(t)

	res, err := Run(context.Background(), Options{
		ServerURL:    f.srv.URL,
		Token:        "sfen_xxx",
		Name:         "laptop-1",
		Hostname:     "h.local",
		OS:           "linux",
		Arch:         "amd64",
		AgentVersion: "0.1.0",
		ConfigPath:   cfgPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.DeviceID != "dev_abc" || res.Status != "pending" {
		t.Errorf("result = %+v", res)
	}

	// Body forwarded with the operator-supplied fields verbatim.
	if f.gotBody["token"] != "sfen_xxx" || f.gotBody["name"] != "laptop-1" ||
		f.gotBody["hostname"] != "h.local" || f.gotBody["os"] != "linux" {
		t.Errorf("body forwarded incorrectly: %+v", f.gotBody)
	}
	if ct := f.gotHeaders.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %s", ct)
	}

	// agent.json landed at expected path with 0600.
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %v", info.Mode().Perm())
	}
	// File is loadable and intervals defaulted.
	loaded, err := agentcfg.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DeviceID != "dev_abc" || loaded.DeviceSecret != "s3cret" {
		t.Errorf("persisted cfg = %+v", loaded)
	}
	if loaded.HeartbeatIntSec != agentcfg.DefaultHeartbeatSec ||
		loaded.InventoryIntSec != agentcfg.DefaultInventorySec {
		t.Errorf("interval defaults not applied: %+v", loaded)
	}
	if loaded.ServerURL != f.srv.URL {
		t.Errorf("server_url = %s, want %s", loaded.ServerURL, f.srv.URL)
	}
}

func TestRun_AutoDetectsHostMetadata(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.json")
	f := newFake(t)

	if _, err := Run(context.Background(), Options{
		ServerURL:  f.srv.URL,
		Token:      "sfen_x",
		Name:       "n",
		ConfigPath: cfgPath,
	}); err != nil {
		t.Fatal(err)
	}
	// hostname / os / arch / agent_version all auto-filled (non-empty).
	for _, k := range []string{"os", "arch", "agent_version"} {
		if v, _ := f.gotBody[k].(string); v == "" {
			t.Errorf("auto-detect field %s empty", k)
		}
	}
	// Hostname only set if os.Hostname() succeeded; just assert the
	// key is present (may be "").
	if _, ok := f.gotBody["hostname"]; !ok {
		t.Errorf("hostname key missing")
	}
}

func TestRun_TrimsTrailingSlash(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.json")
	f := newFake(t)

	if _, err := Run(context.Background(), Options{
		ServerURL:  f.srv.URL + "/",
		Token:      "t",
		Name:       "n",
		ConfigPath: cfgPath,
	}); err != nil {
		t.Fatal(err)
	}
	if f.hits != 1 {
		t.Errorf("hits = %d, want 1 (trailing slash should have hit /agent/enroll cleanly)", f.hits)
	}
	loaded, _ := agentcfg.Load(cfgPath)
	if strings.HasSuffix(loaded.ServerURL, "/") {
		t.Errorf("persisted server_url retained trailing slash: %s", loaded.ServerURL)
	}
}

func TestRun_RequiresFields(t *testing.T) {
	cases := []Options{
		{ServerURL: "", Token: "t", Name: "n"},
		{ServerURL: "http://x", Token: "", Name: "n"},
		{ServerURL: "http://x", Token: "t", Name: ""},
		{ServerURL: "http://x", Token: "  ", Name: "n"},
	}
	for i, opt := range cases {
		opt.ConfigPath = filepath.Join(t.TempDir(), "a.json")
		if _, err := Run(context.Background(), opt); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestRun_RejectsBadScheme(t *testing.T) {
	if _, err := Run(context.Background(), Options{
		ServerURL:  "ftp://x.example.com",
		Token:      "t",
		Name:       "n",
		ConfigPath: filepath.Join(t.TempDir(), "a.json"),
	}); err == nil {
		t.Error("expected error for ftp scheme")
	}
}

func TestRun_MapsTokenErrors(t *testing.T) {
	cases := []struct {
		code string
		want error
	}{
		{"token_not_found", ErrTokenNotFound},
		{"token_expired", ErrTokenExpired},
		{"token_not_usable", ErrTokenNotUsable},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			f := newFake(t)
			f.respStatus = http.StatusForbidden
			f.respBody = map[string]any{"error": c.code, "message": "x"}

			_, err := Run(context.Background(), Options{
				ServerURL:  f.srv.URL,
				Token:      "t",
				Name:       "n",
				ConfigPath: filepath.Join(t.TempDir(), "a.json"),
			})
			if !errors.Is(err, c.want) {
				t.Errorf("err = %v, want Is %v", err, c.want)
			}
		})
	}
}

func TestRun_PreservesUnknownErrorMessage(t *testing.T) {
	f := newFake(t)
	f.respStatus = http.StatusInternalServerError
	f.respBody = map[string]any{"error": "internal_error", "message": "kaboom"}

	_, err := Run(context.Background(), Options{
		ServerURL:  f.srv.URL,
		Token:      "t",
		Name:       "n",
		ConfigPath: filepath.Join(t.TempDir(), "a.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "kaboom") {
		t.Errorf("err = %v", err)
	}
}

func TestRun_RefusesExistingConfigBeforeHitting(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(cfgPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFake(t)

	_, err := Run(context.Background(), Options{
		ServerURL:  f.srv.URL,
		Token:      "t",
		Name:       "n",
		ConfigPath: cfgPath,
	})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Errorf("err = %v, want ErrAlreadyExists", err)
	}
	if f.hits != 0 {
		t.Errorf("server hit %d times; pre-flight must short-circuit before POST", f.hits)
	}
}

func TestRun_RejectsEmptyDeviceFields(t *testing.T) {
	f := newFake(t)
	f.respBody = map[string]any{
		"device_id":     "",
		"device_secret": "s",
		"status":        "pending",
	}
	_, err := Run(context.Background(), Options{
		ServerURL:  f.srv.URL,
		Token:      "t",
		Name:       "n",
		ConfigPath: filepath.Join(t.TempDir(), "a.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "empty device_id") {
		t.Errorf("err = %v", err)
	}
}

func TestRun_RespectsContextCancel(t *testing.T) {
	// httptest server that never responds within the test deadline.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := Run(ctx, Options{
		ServerURL:  srv.URL,
		Token:      "t",
		Name:       "n",
		ConfigPath: filepath.Join(t.TempDir(), "a.json"),
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
	})
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestBuildEnrollURL(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"http://x.test", "http://x.test/agent/enroll", false},
		{"http://x.test/", "http://x.test/agent/enroll", false},
		{"https://x.test/api/", "https://x.test/api/agent/enroll", false},
		{"file:///etc/passwd", "", true},
		{"://broken", "", true},
		{"http://", "", true},
	}
	for _, c := range cases {
		got, err := buildEnrollURL(c.in)
		if c.wantErr && err == nil {
			t.Errorf("%s: expected error", c.in)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("%s: got %s, want %s", c.in, got, c.want)
		}
	}
}
