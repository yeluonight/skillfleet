package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestDraftFile_UTF8ChineseRoundTrip verifies that a normal UTF-8 file
// with CJK characters round-trips correctly through PUT then GET.
func TestDraftFile_UTF8ChineseRoundTrip(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "files")

	url := srv.URL + "/api/skill-drafts/" + id + "/files/SKILL.md"
	chineseContent := "---\nname: files\ndescription: 中文描述\n---\n# 标题\n\n这是正文。\n"
	req := newJSONReq(t, http.MethodPut, url, map[string]string{"content": chineseContent})
	resp := authedDo(t, sc, cc, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT = %d, want 200", resp.StatusCode)
	}

	// GET the draft and verify content matches.
	greq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-drafts/"+id, nil)
	gresp := authedDo(t, sc, cc, greq)
	defer gresp.Body.Close()
	var got draftView
	if err := json.NewDecoder(gresp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range got.Files {
		if f.Path == "SKILL.md" {
			found = true
			if f.Content != chineseContent {
				t.Errorf("content mismatch:\ngot:  %q\nwant: %q", f.Content, chineseContent)
			}
		}
	}
	if !found {
		t.Error("SKILL.md missing after PUT")
	}
}

// TestDraftFile_RejectsBOMWithoutConvert verifies that a PUT request
// whose content begins with a UTF-8 BOM is rejected with 422 and
// error code "encoding_bom" when convert_to_utf8 is not set.
func TestDraftFile_RejectsBOMWithoutConvert(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "files")

	// Build BOM string from bytes to avoid a literal BOM in source.
	bom := string([]byte{0xEF, 0xBB, 0xBF})
	bomContent := bom + "---\nname: files\ndescription: d\n---\n# x\n"

	url := srv.URL + "/api/skill-drafts/" + id + "/files/SKILL.md"
	req := newJSONReq(t, http.MethodPut, url, map[string]string{"content": bomContent})
	resp := authedDo(t, sc, cc, req)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("PUT with BOM = %d, want 422", resp.StatusCode)
	}

	var errResp apiError
	if err := json.NewDecoder(resp.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp.Error != "encoding_bom" {
		t.Errorf("error code = %q, want %q", errResp.Error, "encoding_bom")
	}
}

// TestDraftFile_StripsBOMWithConvert verifies that a PUT request with
// convert_to_utf8=true and BOM-prefixed content succeeds (200) and the
// stored content has the BOM stripped (the first character is the one
// after the BOM, not the BOM itself).
func TestDraftFile_StripsBOMWithConvert(t *testing.T) {
	srv, d, _ := newTestServerWithRegistry(t)
	sc, cc := setupAndLogin(t, srv, d, "alice", "correcthorsebatterystaple")
	id := createDraftForTest(t, srv.URL, sc, cc, "files")

	// Build BOM string from bytes to avoid a literal BOM in source.
	bom := string([]byte{0xEF, 0xBB, 0xBF})
	bodyAfterBOM := "---\nname: files\ndescription: d\n---\n# x\n"
	bomContent := bom + bodyAfterBOM

	url := srv.URL + "/api/skill-drafts/" + id + "/files/SKILL.md"
	req := newJSONReq(t, http.MethodPut, url, map[string]any{
		"content":         bomContent,
		"convert_to_utf8": true,
	})
	resp := authedDo(t, sc, cc, req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT with convert_to_utf8 = %d, want 200", resp.StatusCode)
	}

	// GET the draft and verify the BOM was stripped.
	greq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/skill-drafts/"+id, nil)
	gresp := authedDo(t, sc, cc, greq)
	defer gresp.Body.Close()
	var got draftView
	if err := json.NewDecoder(gresp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range got.Files {
		if f.Path == "SKILL.md" {
			found = true
			if strings.HasPrefix(f.Content, bom) {
				t.Errorf("content still starts with BOM: %q", f.Content)
			}
			if f.Content != bodyAfterBOM {
				t.Errorf("content mismatch after BOM strip:\ngot:  %q\nwant: %q", f.Content, bodyAfterBOM)
			}
		}
	}
	if !found {
		t.Error("SKILL.md missing after PUT")
	}
}
