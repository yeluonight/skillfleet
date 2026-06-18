package agentapi

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yeluonight/skillfleet/internal/deploy"
)

// fakeAdopter records the last adoption and returns a canned version id.
type fakeAdopter struct {
	gotName   string
	gotFiles  []deploy.AdoptFile
	gotSource deploy.AdoptSource
	versionID string
	err       error
}

func (a *fakeAdopter) AdoptSkill(name string, files []deploy.AdoptFile, source deploy.AdoptSource) (string, error) {
	a.gotName = name
	a.gotFiles = files
	a.gotSource = source
	if a.err != nil {
		return "", a.err
	}
	return a.versionID, nil
}

// newUploadFixture is a jobsFixture whose router also has a SkillAdopter.
func newUploadFixture(t *testing.T, adopter SkillAdopter) *jobsFixture {
	t.Helper()
	f := newJobsFixture(t, fakePackages{})
	// Re-wire the server with an adopter (newJobsFixture didn't set one).
	deps := Deps{
		DB:      f.db,
		Now:     func() time.Time { return f.now },
		Adopter: adopter,
	}
	f.srv.Close()
	f.srv = httptest.NewServer(NewRouter(deps))
	t.Cleanup(f.srv.Close)
	return f
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// TestUpload_AdoptsSkill: a signed POST with files publishes via the adopter
// and returns the new version id; the device id comes from the signed
// context, not the body.
func TestUpload_AdoptsSkill(t *testing.T) {
	ad := &fakeAdopter{versionID: "sv_new"}
	f := newUploadFixture(t, ad)

	body, _ := json.Marshal(deploy.UploadRequest{
		SkillName: "deploy-helper",
		Files:     []deploy.UploadFile{{Path: "SKILL.md", ContentBase64: b64("# hi")}},
		Source:    deploy.AdoptSource{DeviceID: "forged", ToolKey: "claude-code", Scope: "user"},
	})
	resp, err := http.DefaultClient.Do(f.signed(t, http.MethodPost, "/agent/upload", body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
	var got deploy.UploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.VersionID != "sv_new" {
		t.Errorf("version id = %q", got.VersionID)
	}
	if ad.gotName != "deploy-helper" || len(ad.gotFiles) != 1 {
		t.Errorf("adopter got name=%q files=%d", ad.gotName, len(ad.gotFiles))
	}
	// Device id must be overridden from the signed context, not the body.
	if ad.gotSource.DeviceID == "forged" || ad.gotSource.DeviceID != f.deviceID {
		t.Errorf("source device id = %q, want signed %q", ad.gotSource.DeviceID, f.deviceID)
	}
	if ad.gotSource.ToolKey != "claude-code" {
		t.Errorf("source tool_key = %q", ad.gotSource.ToolKey)
	}
}

// TestUpload_BadBase64: a malformed base64 body is rejected 400 before the
// adopter is called.
func TestUpload_BadBase64(t *testing.T) {
	ad := &fakeAdopter{versionID: "sv_x"}
	f := newUploadFixture(t, ad)

	body, _ := json.Marshal(deploy.UploadRequest{
		SkillName: "s",
		Files:     []deploy.UploadFile{{Path: "a", ContentBase64: "!!!not base64!!!"}},
	})
	resp, err := http.DefaultClient.Do(f.signed(t, http.MethodPost, "/agent/upload", body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if ad.gotName != "" {
		t.Errorf("adopter should not have been called, got name=%q", ad.gotName)
	}
}

// TestUpload_NoAdopter: when no adopter is configured the route returns 503.
func TestUpload_NoAdopter(t *testing.T) {
	f := newUploadFixture(t, nil)
	body, _ := json.Marshal(deploy.UploadRequest{
		SkillName: "s",
		Files:     []deploy.UploadFile{{Path: "a", ContentBase64: b64("x")}},
	})
	resp, err := http.DefaultClient.Do(f.signed(t, http.MethodPost, "/agent/upload", body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// TestUpload_LargeBodyAccepted: a body over the default 1 MiB cap is accepted
// on /agent/upload (which uses MaxAgentUploadBytes), proving the route-scoped
// limit raise works.
func TestUpload_LargeBodyAccepted(t *testing.T) {
	ad := &fakeAdopter{versionID: "sv_big"}
	f := newUploadFixture(t, ad)

	big := strings.Repeat("A", 2<<20) // 2 MiB of content, > default 1 MiB cap
	body, _ := json.Marshal(deploy.UploadRequest{
		SkillName: "big",
		Files:     []deploy.UploadFile{{Path: "data.bin", ContentBase64: b64(big)}},
	})
	resp, err := http.DefaultClient.Do(f.signed(t, http.MethodPost, "/agent/upload", body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, raw)
	}
}
