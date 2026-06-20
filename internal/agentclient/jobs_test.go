package agentclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/sfhmac"
)

type jobsFakeServer struct {
	srv       *httptest.Server
	deviceID  string
	plaintext string

	jobsStatus int
	jobsBody   map[string]any

	packageStatus int
	packageBody   []byte
	packageJSON   map[string]any

	resultStatus int
	resultBody   map[string]any

	gotJobsMethod   string
	gotJobsHeaders  http.Header
	gotResultBody   deploy.JobResult
	gotResultBodyOK bool
	verifyError     error
}

func newJobsFakeServer(t *testing.T) *jobsFakeServer {
	t.Helper()
	f := &jobsFakeServer{
		deviceID:      "dev_abc",
		plaintext:     "plaintext-secret",
		jobsStatus:    http.StatusNoContent,
		packageStatus: http.StatusOK,
		resultStatus:  http.StatusOK,
		resultBody:    map[string]any{"status": "ok"},
	}

	verify := func(w http.ResponseWriter, r *http.Request) bool {
		hdr, err := sfhmac.Parse(r)
		if err != nil {
			f.verifyError = err
			http.Error(w, "missing headers", http.StatusUnauthorized)
			return false
		}
		key := devices.HMACKey(f.plaintext)
		if err := sfhmac.Verify(key, r.Method, r.URL.Path, hdr.Timestamp, hdr.Nonce, hdr.BodyHash, hdr.Signature); err != nil {
			f.verifyError = err
			http.Error(w, "bad sig", http.StatusUnauthorized)
			return false
		}
		return true
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /agent/jobs", func(w http.ResponseWriter, r *http.Request) {
		f.gotJobsMethod = r.Method
		f.gotJobsHeaders = r.Header.Clone()
		if !verify(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.jobsStatus)
		if f.jobsBody != nil {
			_ = json.NewEncoder(w).Encode(f.jobsBody)
		}
	})
	mux.HandleFunc("GET /agent/packages/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !verify(w, r) {
			return
		}
		if f.packageJSON != nil {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(f.packageStatus)
		if f.packageJSON != nil {
			_ = json.NewEncoder(w).Encode(f.packageJSON)
			return
		}
		_, _ = w.Write(f.packageBody)
	})
	mux.HandleFunc("POST /agent/jobs/{id}/result", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if len(body) > 0 {
			if err := json.Unmarshal(body, &f.gotResultBody); err == nil {
				f.gotResultBodyOK = true
			}
		}
		if !verify(w, r) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.resultStatus)
		if f.resultBody != nil {
			_ = json.NewEncoder(w).Encode(f.resultBody)
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestJobs_ClaimsJob(t *testing.T) {
	f := newJobsFakeServer(t)
	f.jobsStatus = http.StatusOK
	f.jobsBody = map[string]any{
		"id":           "dj_1",
		"operation":    "install",
		"request_json": "{}",
		"plan_json":    `{"version_id":"sv_1"}`,
	}
	c, err := New(Config{ServerURL: f.srv.URL, DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	if err != nil {
		t.Fatal(err)
	}

	job, ok, err := c.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if job.ID != "dj_1" || job.Operation != "install" || job.PlanJSON == "" {
		t.Errorf("job = %+v", job)
	}
	if f.gotJobsMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", f.gotJobsMethod)
	}
	if f.verifyError != nil {
		t.Errorf("server-side verify error: %v", f.verifyError)
	}
	for _, h := range []string{sfhmac.HeaderDeviceID, sfhmac.HeaderTimestamp, sfhmac.HeaderNonce, sfhmac.HeaderBodyHash, sfhmac.HeaderSignature} {
		if f.gotJobsHeaders.Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
}

func TestJobs_NoContent(t *testing.T) {
	f := newJobsFakeServer(t)
	f.jobsStatus = http.StatusNoContent
	c, err := New(Config{ServerURL: f.srv.URL, DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	if err != nil {
		t.Fatal(err)
	}

	_, ok, err := c.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
}

func TestJobs_ServerError(t *testing.T) {
	f := newJobsFakeServer(t)
	f.jobsStatus = http.StatusInternalServerError
	f.jobsBody = map[string]any{"error": "internal_error", "message": "x"}
	c, err := New(Config{ServerURL: f.srv.URL, DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	if err != nil {
		t.Fatal(err)
	}

	_, ok, err := c.Jobs(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
}

func TestDownloadPackage_Streams(t *testing.T) {
	f := newJobsFakeServer(t)
	f.packageStatus = http.StatusOK
	f.packageBody = []byte("ARCHIVE BYTES")
	c, err := New(Config{ServerURL: f.srv.URL, DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	if err != nil {
		t.Fatal(err)
	}

	r, err := c.DownloadPackage(context.Background(), "/agent/packages/sv_1")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ARCHIVE BYTES" {
		t.Errorf("body = %q", string(got))
	}
}

func TestDownloadPackage_NotFound(t *testing.T) {
	f := newJobsFakeServer(t)
	f.packageStatus = http.StatusNotFound
	f.packageJSON = map[string]any{"error": "not_found", "message": "x"}
	c, err := New(Config{ServerURL: f.srv.URL, DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	if err != nil {
		t.Fatal(err)
	}

	r, err := c.DownloadPackage(context.Background(), "/agent/packages/sv_1")
	if err == nil {
		if r != nil {
			r.Close()
		}
		t.Fatal("expected error")
	}
	if r != nil {
		t.Fatal("reader should be nil on error")
	}
}

func TestJobResult_Posts(t *testing.T) {
	f := newJobsFakeServer(t)
	f.resultStatus = http.StatusOK
	f.resultBody = map[string]any{"status": "ok"}
	c, err := New(Config{ServerURL: f.srv.URL, DeviceID: f.deviceID, DeviceSecret: f.plaintext})
	if err != nil {
		t.Fatal(err)
	}

	err = c.JobResult(context.Background(), "dj_1", deploy.JobResult{Status: "succeeded", ResultJSON: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.gotResultBodyOK {
		t.Fatal("result body was not valid JSON")
	}
	if f.gotResultBody.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", f.gotResultBody.Status)
	}
	if strings.TrimSpace(f.gotResultBody.ResultJSON) != "{}" {
		t.Errorf("result_json = %q", f.gotResultBody.ResultJSON)
	}
}
