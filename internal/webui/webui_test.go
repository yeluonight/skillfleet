package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newClient(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestServesIndexAtRoot(t *testing.T) {
	srv := newClient(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "SkillFleet") {
		t.Errorf("index html missing SkillFleet marker: %.200s", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestSPAFallbackForUnknownRoute(t *testing.T) {
	srv := newClient(t)
	resp, err := http.Get(srv.URL + "/dashboard")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (SPA fallback)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "SkillFleet") {
		t.Errorf("SPA fallback didn't return index html: %.200s", body)
	}
}

func TestAssetShapedMissesReturn404(t *testing.T) {
	srv := newClient(t)
	resp, err := http.Get(srv.URL + "/assets/missing-XYZ.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for asset-shaped miss", resp.StatusCode)
	}
}

func TestRejectsPathTraversal(t *testing.T) {
	srv := newClient(t)
	// http.Client normalises the path before sending so `/../../etc/passwd`
	// reaches the server as `/etc/passwd`. Either way, the handler MUST
	// NOT return content from outside the embedded bundle. We assert the
	// negative: the response body must look like the WebUI shell, not a
	// system file. (200 with the SPA fallback is acceptable; what we
	// care about is "you cannot exfiltrate host files via the WebUI".)
	resp, err := http.Get(srv.URL + "/../../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), "root:") {
		t.Errorf("traversal exfiltrated /etc/passwd content: %.200s", body)
	}
	if resp.StatusCode == http.StatusOK && !strings.Contains(string(body), "SkillFleet") {
		t.Errorf("non-fallback 200 with foreign body: %.200s", body)
	}
}

func TestRejectsNonGet(t *testing.T) {
	srv := newClient(t)
	resp, err := http.Post(srv.URL+"/some/path", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
	if allow := resp.Header.Get("Allow"); allow == "" {
		t.Error("missing Allow header on 405")
	}
}

func TestHeadReturnsHeadersOnly(t *testing.T) {
	srv := newClient(t)
	req, _ := http.NewRequest(http.MethodHead, srv.URL+"/missing-route", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) != 0 {
		t.Errorf("HEAD returned %d bytes, want 0", len(body))
	}
}

func TestFSExposesIndex(t *testing.T) {
	f, err := FS().Open("index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
}
