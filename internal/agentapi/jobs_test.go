package agentapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/db"
	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/enrollment"
	"github.com/yeluonight/skillfleet/internal/sfhmac"
	"github.com/yeluonight/skillfleet/migrations"
)

// fakePackages implements PackageSource over a temp .tgz file, so the
// packages handler can be tested without the registry.
type fakePackages struct {
	versionID string
	path      string // the .tgz on disk; empty → always not found
}

func (f fakePackages) ArchiveForVersion(versionID string) (*os.File, int64, error) {
	if f.path == "" || versionID != f.versionID {
		return nil, 0, ErrPackageNotFound
	}
	fh, err := os.Open(f.path)
	if err != nil {
		return nil, 0, err
	}
	info, _ := fh.Stat()
	return fh, info.Size(), nil
}

// jobsFixture wires the full downlink router (real jobs routes + a fake
// PackageSource) against one enrolled, approved device.
type jobsFixture struct {
	srv      *httptest.Server
	db       *sql.DB
	store    *deploy.Store
	deviceID string
	hmacKey  string
	now      time.Time
	pkg      fakePackages
}

func newJobsFixture(t *testing.T, pkg fakePackages) *jobsFixture {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrations.Apply(ctx, d, migrations.Embedded()); err != nil {
		t.Fatal(err)
	}
	f := &jobsFixture{db: d, store: deploy.New(d), now: time.Unix(1_700_000_000, 0), pkg: pkg}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := enrollment.Create(ctx, d, time.Hour, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enrollment.Consume(ctx, tx, tok.Plaintext, f.now); err != nil {
		t.Fatal(err)
	}
	res, err := devices.Enroll(ctx, tx, devices.EnrollInput{Name: "n", OS: "linux", Arch: "amd64"}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := devices.SetStatus(ctx, d, res.Device.ID, devices.StatusApproved); err != nil {
		t.Fatal(err)
	}
	f.deviceID = res.Device.ID
	f.hmacKey = devices.HMACKey(res.Secret)

	deps := Deps{
		DB:       d,
		Now:      func() time.Time { return f.now },
		Audit:    audit.New(d, nil, func() time.Time { return f.now }),
		Packages: pkg,
	}
	f.srv = httptest.NewServer(NewRouter(deps))
	t.Cleanup(func() {
		f.srv.Close()
		_ = d.Close()
	})
	return f
}

// signed builds an HMAC-signed request for the downlink routes.
func (f *jobsFixture) signed(t *testing.T, method, path string, body []byte) *http.Request {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if err := sfhmac.SignRequest(req, f.deviceID, f.hmacKey, "", f.now, body); err != nil {
		t.Fatal(err)
	}
	return req
}

// seedJob inserts a pending install job for the fixture's device.
func (f *jobsFixture) seedJob(t *testing.T) deploy.Job {
	t.Helper()
	job, err := f.store.Create(context.Background(), deploy.CreateParams{
		DeviceID:    f.deviceID,
		Operation:   deploy.OpInstall,
		RequestJSON: `{"operation":"install","skill_name":"deploy-helper","version_id":"sv_1"}`,
		PlanJSON:    `{"version_id":"sv_1","skill_name":"deploy-helper"}`,
	}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// TestGetJobs_ClaimsPending: a signed GET returns the device's pending
// job (200 + plan), and a second GET returns 204 (already claimed).
func TestGetJobs_ClaimsPending(t *testing.T) {
	f := newJobsFixture(t, fakePackages{})
	job := f.seedJob(t)

	resp, err := http.DefaultClient.Do(f.signed(t, http.MethodGet, "/agent/jobs", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	var got deploy.ClaimedJob
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != job.ID || got.Operation != "install" || got.PlanJSON == "" {
		t.Errorf("claimed job = %+v", got)
	}

	// Second claim: nothing pending → 204.
	resp2, err := http.DefaultClient.Do(f.signed(t, http.MethodGet, "/agent/jobs", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Errorf("second claim status = %d, want 204", resp2.StatusCode)
	}
}

// TestGetJobs_OnlyOwnDevice: a job for another device is never handed to
// this device's claim (acceptance #1 scoping).
func TestGetJobs_OnlyOwnDevice(t *testing.T) {
	f := newJobsFixture(t, fakePackages{})
	// Seed a second device + a job for it.
	ctx := context.Background()
	if _, err := f.db.ExecContext(ctx,
		`INSERT INTO devices(id,name,status,created_at) VALUES ('dev_other','o','approved',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Create(ctx, deploy.CreateParams{
		DeviceID: "dev_other", Operation: deploy.OpInstall, RequestJSON: "{}",
	}, f.now); err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(f.signed(t, http.MethodGet, "/agent/jobs", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (must not claim another device's job)", resp.StatusCode)
	}
}

func TestGetJobs_RequiresAuth(t *testing.T) {
	f := newJobsFixture(t, fakePackages{})
	resp, err := http.Get(f.srv.URL + "/agent/jobs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unsigned GET status = %d, want 401", resp.StatusCode)
	}
}

// TestJobResult_CompletesClaimed: claim then report succeeded → job is
// terminal with the result stored.
func TestJobResult_CompletesClaimed(t *testing.T) {
	f := newJobsFixture(t, fakePackages{})
	job := f.seedJob(t)
	// Claim it first.
	http.DefaultClient.Do(f.signed(t, http.MethodGet, "/agent/jobs", nil))

	body := []byte(`{"status":"succeeded","result_json":"{\"rescan_content_sha256\":\"abc\"}"}`)
	resp, err := http.DefaultClient.Do(f.signed(t, http.MethodPost, "/agent/jobs/"+job.ID+"/result", body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	got, _ := f.store.Get(context.Background(), job.ID)
	if got.Status != deploy.StatusSucceeded || got.ResultJSON == "" {
		t.Errorf("job after result: status=%q result=%q", got.Status, got.ResultJSON)
	}
}

// TestJobResult_RejectsForeignJob: a device cannot complete another
// device's job — it gets a 404 (existence not revealed).
func TestJobResult_RejectsForeignJob(t *testing.T) {
	f := newJobsFixture(t, fakePackages{})
	ctx := context.Background()
	if _, err := f.db.ExecContext(ctx,
		`INSERT INTO devices(id,name,status,created_at) VALUES ('dev_other','o','approved',1)`); err != nil {
		t.Fatal(err)
	}
	other, err := f.store.Create(ctx, deploy.CreateParams{
		DeviceID: "dev_other", Operation: deploy.OpInstall, RequestJSON: "{}",
	}, f.now)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"status":"succeeded","result_json":"{}"}`)
	resp, err := http.DefaultClient.Do(f.signed(t, http.MethodPost, "/agent/jobs/"+other.ID+"/result", body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("completing a foreign job status = %d, want 404", resp.StatusCode)
	}
}

// TestJobResult_RejectsUnclaimed: reporting on a pending (never claimed)
// job is a 409 conflict.
func TestJobResult_RejectsUnclaimed(t *testing.T) {
	f := newJobsFixture(t, fakePackages{})
	job := f.seedJob(t) // pending, not claimed
	body := []byte(`{"status":"succeeded","result_json":"{}"}`)
	resp, err := http.DefaultClient.Do(f.signed(t, http.MethodPost, "/agent/jobs/"+job.ID+"/result", body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("reporting on unclaimed job status = %d, want 409", resp.StatusCode)
	}
}

// TestGetPackage_ServesArchive: a signed GET for a known version streams
// the archive bytes; an unknown version is 404.
func TestGetPackage_ServesArchive(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "pkg.tgz")
	content := []byte("fake archive bytes")
	if err := os.WriteFile(archive, content, 0o644); err != nil {
		t.Fatal(err)
	}
	f := newJobsFixture(t, fakePackages{versionID: "sv_1", path: archive})

	resp, err := http.DefaultClient.Do(f.signed(t, http.MethodGet, "/agent/packages/sv_1", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, content) {
		t.Errorf("served bytes = %q, want %q", got, content)
	}

	// Unknown version → 404.
	resp2, err := http.DefaultClient.Do(f.signed(t, http.MethodGet, "/agent/packages/sv_ghost", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("unknown package status = %d, want 404", resp2.StatusCode)
	}
}

func TestGetPackage_RequiresAuth(t *testing.T) {
	f := newJobsFixture(t, fakePackages{versionID: "sv_1", path: "x"})
	resp, err := http.Get(f.srv.URL + "/agent/packages/sv_1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unsigned package GET status = %d, want 401", resp.StatusCode)
	}
}
