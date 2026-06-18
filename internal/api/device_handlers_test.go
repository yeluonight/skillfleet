package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/enrollment"
	"github.com/yeluonight/skillfleet/internal/ratelimit"
)

// enrollViaDB creates an approved-or-pending device directly so the
// /api/devices tests don't need to drive the agent enrolment path.
// The state machine is exercised through devices.SetStatus, so this
// keeps test setup small while still going through the real package
// (no raw SQL inserts).
func enrollDeviceViaDB(t *testing.T, d *sql.DB, name, startStatus string) string {
	t.Helper()
	return enrollDeviceViaDBAt(t, d, name, startStatus, time.Now())
}

func enrollDeviceViaDBAt(t *testing.T, d *sql.DB, name, startStatus string, now time.Time) string {
	t.Helper()
	ctx := context.Background()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := enrollment.Create(ctx, d, time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enrollment.Consume(ctx, tx, tok.Plaintext, now); err != nil {
		t.Fatal(err)
	}
	res, err := devices.Enroll(ctx, tx, devices.EnrollInput{
		Name: name, Hostname: "h", OS: "linux", Arch: "amd64", AgentVersion: "0.1.0",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if startStatus != "" && startStatus != devices.StatusPending {
		if err := devices.SetStatus(ctx, d, res.Device.ID, startStatus); err != nil {
			t.Fatal(err)
		}
	}
	return res.Device.ID
}

func TestListDevices_ReturnsAll(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	created := time.Now()
	id1 := enrollDeviceViaDBAt(t, d, "laptop-a", devices.StatusPending, created)
	id2 := enrollDeviceViaDBAt(t, d, "laptop-b", devices.StatusApproved, created.Add(time.Millisecond))

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var got struct {
		Devices []deviceView `json:"devices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Devices) != 2 {
		t.Fatalf("devices count = %d, want 2", len(got.Devices))
	}
	// Newest first (ORDER BY created_at DESC).
	if got.Devices[0].ID != id2 || got.Devices[1].ID != id1 {
		t.Errorf("order off: %s / %s", got.Devices[0].ID, got.Devices[1].ID)
	}
	if got.Devices[1].Status != devices.StatusPending || got.Devices[0].Status != devices.StatusApproved {
		t.Errorf("status fields: %+v", got.Devices)
	}
	// Hostname/os/arch present even when device hasn't reported.
	if got.Devices[0].Hostname != "h" || got.Devices[0].OS != "linux" {
		t.Errorf("metadata missing: %+v", got.Devices[0])
	}
	// LastSeenAt is nil for a fresh device.
	if got.Devices[0].LastSeenAt != nil {
		t.Errorf("LastSeenAt should be nil for fresh device, got %v", got.Devices[0].LastSeenAt)
	}
}

func TestGetDevice_ReturnsOneAnd404(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := enrollDeviceViaDB(t, d, "laptop-a", devices.StatusApproved)

	// Existing device → 200 + single view (not wrapped in {devices:[]}).
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices/"+id, nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got deviceView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != id || got.Name != "laptop-a" || got.Status != devices.StatusApproved {
		t.Errorf("device view mismatch: %+v", got)
	}

	// Missing device → 404 not_found.
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices/does-not-exist", nil)
	resp2 := authedDo(t, sc, cc, req2)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("missing device status = %d, want 404", resp2.StatusCode)
	}
}

func TestGetDevice_RequiresAuth(t *testing.T) {
	srv, _ := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	resp, err := http.Get(srv.URL + "/api/devices/anything")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestListDevices_RequiresAuth(t *testing.T) {
	srv, _ := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	resp, err := http.Get(srv.URL + "/api/devices")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestApproveDevice_FlipsPendingToApproved(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := enrollDeviceViaDB(t, d, "laptop", devices.StatusPending)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/devices/"+id+"/approve", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}
	var got deviceView
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if got.Status != devices.StatusApproved {
		t.Errorf("response status = %s", got.Status)
	}

	// DB row updated.
	var st string
	_ = d.QueryRow(`SELECT status FROM devices WHERE id=?`, id).Scan(&st)
	if st != devices.StatusApproved {
		t.Errorf("DB status = %s", st)
	}

	// Audit row recorded with from/to detail.
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='device.approved' AND target_id=?`, id).Scan(&n)
	if n != 1 {
		t.Errorf("audit count = %d", n)
	}
}

func TestApproveDevice_IdempotentOnAlreadyApproved(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := enrollDeviceViaDB(t, d, "laptop", devices.StatusApproved)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/devices/"+id+"/approve", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 idempotent", resp.StatusCode)
	}
	// No audit row should be written on the no-op.
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='device.approved'`).Scan(&n)
	if n != 0 {
		t.Errorf("audit count = %d, want 0 on idempotent no-op", n)
	}
}

func TestApproveDevice_RejectsRevokedDevice(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := enrollDeviceViaDB(t, d, "laptop", devices.StatusPending)
	// Move to revoked via the supported path (pending -> revoked).
	if err := devices.SetStatus(context.Background(), d, id, devices.StatusRevoked); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/devices/"+id+"/approve", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, body=%s, want 409", resp.StatusCode, body)
	}
}

func TestRevokeDevice_FlipsApprovedToRevoked(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := enrollDeviceViaDB(t, d, "laptop", devices.StatusApproved)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/devices/"+id+"/revoke", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}
	var st string
	_ = d.QueryRow(`SELECT status FROM devices WHERE id=?`, id).Scan(&st)
	if st != devices.StatusRevoked {
		t.Errorf("DB status = %s", st)
	}
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='device.revoked' AND target_id=?`, id).Scan(&n)
	if n != 1 {
		t.Errorf("audit count = %d", n)
	}
}

func TestRevokeDevice_FlipsPendingToRevoked(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := enrollDeviceViaDB(t, d, "laptop", devices.StatusPending)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/devices/"+id+"/revoke", nil)
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestDeviceMutation_UnknownIDReturns404(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")

	for _, path := range []string{"/approve", "/revoke"} {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/devices/dev_nope"+path, nil)
		resp := authedDo(t, sc, cc, req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestDeviceMutation_RequiresCSRF(t *testing.T) {
	srv, d := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := enrollDeviceViaDB(t, d, "laptop", devices.StatusPending)

	// Send the session + csrf cookie but NOT the matching header.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/devices/"+id+"/approve", nil)
	req.AddCookie(sc)
	req.AddCookie(cc)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestDeviceMutation_RequiresAuth(t *testing.T) {
	srv, _ := newTestServerWithLimits(t,
		ratelimit.Rate{Limit: 100, Window: time.Minute},
		ratelimit.Rate{Limit: 100, Window: time.Minute},
	)
	resp, err := http.Post(srv.URL+"/api/devices/dev_x/approve", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}
