package agentclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/sfhmac"
)

// fakeServer hosts a fake /agent/heartbeat that verifies the signature
// (using the SAME devices.HMACKey contract as the server) and returns
// scripted responses.
type fakeServer struct {
	srv         *httptest.Server
	deviceID    string
	plaintext   string
	respStatus  int
	respBody    map[string]any
	gotHeaders  http.Header
	gotBody     map[string]any
	verifyError error
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{
		deviceID:   "dev_abc",
		plaintext:  "plaintext-secret",
		respStatus: http.StatusOK,
		respBody:   map[string]any{"status": "ok"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/agent/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		f.gotHeaders = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			_ = json.Unmarshal(body, &f.gotBody)
		}
		// Verify the signature using the server-stored key.
		hdr, err := sfhmac.Parse(r)
		if err != nil {
			f.verifyError = err
			http.Error(w, "missing headers", http.StatusUnauthorized)
			return
		}
		key := devices.HMACKey(f.plaintext)
		if err := sfhmac.Verify(key, r.Method, r.URL.Path, hdr.Timestamp, hdr.Nonce, hdr.BodyHash, hdr.Signature); err != nil {
			f.verifyError = err
			http.Error(w, "bad sig", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.respStatus)
		_ = json.NewEncoder(w).Encode(f.respBody)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestHeartbeat_HappyPath(t *testing.T) {
	f := newFakeServer(t)
	c, err := New(Config{
		ServerURL:    f.srv.URL,
		DeviceID:     f.deviceID,
		DeviceSecret: f.plaintext,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Heartbeat(context.Background(), HeartbeatRequest{AgentVersion: "0.1.0"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q", resp.Status)
	}
	if f.verifyError != nil {
		t.Errorf("server-side verify error: %v", f.verifyError)
	}
	// All five sig headers landed.
	for _, h := range []string{sfhmac.HeaderDeviceID, sfhmac.HeaderTimestamp, sfhmac.HeaderNonce, sfhmac.HeaderBodyHash, sfhmac.HeaderSignature} {
		if f.gotHeaders.Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
	// Body forwarded.
	if v, _ := f.gotBody["agent_version"].(string); v != "0.1.0" {
		t.Errorf("agent_version forwarded = %v", v)
	}
}

func TestHeartbeat_EmptyPayloadOK(t *testing.T) {
	f := newFakeServer(t)
	c, _ := New(Config{ServerURL: f.srv.URL, DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	if _, err := c.Heartbeat(context.Background(), HeartbeatRequest{}); err != nil {
		t.Fatal(err)
	}
	if f.verifyError != nil {
		t.Errorf("server-side verify error: %v", f.verifyError)
	}
}

func TestNew_RequiresFields(t *testing.T) {
	cases := []Config{
		{ServerURL: "", DeviceID: "d", DeviceSecret: "s"},
		{ServerURL: "http://x", DeviceID: "", DeviceSecret: "s"},
		{ServerURL: "http://x", DeviceID: "d", DeviceSecret: ""},
	}
	for i, c := range cases {
		if _, err := New(c); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
}

func TestHeartbeat_MapsDeviceNotApproved(t *testing.T) {
	f := newFakeServer(t)
	f.respStatus = http.StatusForbidden
	f.respBody = map[string]any{"error": "device_not_approved", "message": "pending"}
	c, _ := New(Config{ServerURL: f.srv.URL, DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	_, err := c.Heartbeat(context.Background(), HeartbeatRequest{})
	if !errors.Is(err, ErrDeviceNotApproved) {
		t.Errorf("err = %v, want ErrDeviceNotApproved", err)
	}
}

func TestHeartbeat_MapsUnauthorized(t *testing.T) {
	f := newFakeServer(t)
	f.respStatus = http.StatusUnauthorized
	f.respBody = map[string]any{"error": "bad_signature", "message": "bad"}
	c, _ := New(Config{ServerURL: f.srv.URL, DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	_, err := c.Heartbeat(context.Background(), HeartbeatRequest{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestHeartbeat_PreservesUnknownErrorMessage(t *testing.T) {
	f := newFakeServer(t)
	f.respStatus = http.StatusInternalServerError
	f.respBody = map[string]any{"error": "internal_error", "message": "boom"}
	c, _ := New(Config{ServerURL: f.srv.URL, DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	_, err := c.Heartbeat(context.Background(), HeartbeatRequest{})
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v", err)
	}
}

func TestHeartbeat_RespectsContextTimeout(t *testing.T) {
	// Server that hangs forever.
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	t.Cleanup(hang.Close)

	c, _ := New(Config{
		ServerURL:    hang.URL,
		DeviceID:     "dev_x",
		DeviceSecret: "s",
		HTTPClient:   &http.Client{Timeout: 10 * time.Second},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if _, err := c.Heartbeat(ctx, HeartbeatRequest{}); err == nil {
		t.Error("expected ctx timeout error")
	}
}

func TestHeartbeat_TrimsServerURLTrailingSlash(t *testing.T) {
	f := newFakeServer(t)
	c, _ := New(Config{ServerURL: f.srv.URL + "///", DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	if _, err := c.Heartbeat(context.Background(), HeartbeatRequest{}); err != nil {
		t.Fatal(err)
	}
}
