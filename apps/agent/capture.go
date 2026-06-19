// apps/agent — capture.go: the capture_skill job executor (mgmt-refactor
// track A). It reads a discovered skill's real files from a local path the
// server learned from this device's own inventory, and uploads the bytes so
// the server can adopt them into the registry (device -> registry, the
// inverse of install).
//
// Safety: the server sends an absolute path (the skill_path it scanned), the
// same narrow exception register_root makes. The agent re-validates the path
// is a real directory under the user's home before reading anything, so a
// tampered/forged downlink cannot make the agent exfiltrate arbitrary files.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yeluonight/skillfleet/internal/agentclient"
	"github.com/yeluonight/skillfleet/internal/agentroots"
	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/fingerprint"
)

// runCaptureSkillJob reads the skill directory named by req.CapturePath,
// uploads its files, and returns the published version id in the result.
func runCaptureSkillJob(ctx context.Context, client *agentclient.Client, home string, req deploy.Request) (deploy.Result, error) {
	if req.SkillName == "" || req.CapturePath == "" {
		err := errors.New("capture_skill requires skill_name and capture_path")
		return deploy.Result{ErrorCode: "bad_request", ErrorMessage: err.Error()}, err
	}

	abs, err := validateCapturePath(req.CapturePath, home)
	if err != nil {
		return deploy.Result{ErrorCode: "capture_path_invalid", ErrorMessage: err.Error()}, err
	}

	files, err := readSkillFiles(abs)
	if err != nil {
		return deploy.Result{ErrorCode: "capture_read_failed", ErrorMessage: err.Error()}, err
	}
	if len(files) == 0 {
		err := fmt.Errorf("no files found under %s", abs)
		return deploy.Result{ErrorCode: "capture_empty", ErrorMessage: err.Error()}, err
	}

	resp, err := client.UploadSkill(ctx, deploy.UploadRequest{
		SkillName: req.SkillName,
		Files:     files,
		Source: deploy.AdoptSource{
			ToolKey: req.Target.ToolKey,
			Scope:   req.Target.Scope,
			RootID:  req.Target.RootID,
		},
	})
	if err != nil {
		return deploy.Result{ErrorCode: "capture_upload_failed", ErrorMessage: err.Error()}, err
	}
	// Report the resolved path + new version id for operator visibility.
	return deploy.Result{ResolvedRootPath: abs, RescanContentSHA256: resp.VersionID}, nil
}

// validateCapturePath resolves p to an absolute, symlink-free directory and
// confirms it is under home. Reading skill files anywhere else is refused —
// the home subtree is where every skill root lives. It delegates to the same
// agentroots policy register_root uses (ResolveExistingDir + IsHomeDescendant)
// so capture and registration can't disagree on what "under home" means.
func validateCapturePath(p, home string) (string, error) {
	if home == "" {
		return "", errors.New("home dir unknown; cannot validate capture path")
	}
	abs, err := agentroots.ResolveExistingDir(p)
	if err != nil {
		return "", err
	}
	if !agentroots.IsHomeDescendant(abs, home) {
		return "", fmt.Errorf("%q is outside home", abs)
	}
	return abs, nil
}

// readSkillFiles walks the skill directory and returns its files as upload
// entries (base64-encoded). It reuses fingerprint.Compute for the file list
// so the same skip rules apply (hidden files, the .skillfleet marker,
// symlinks inside) — the uploaded set matches what the scanner hashes.
func readSkillFiles(dir string) ([]deploy.UploadFile, error) {
	fp, err := fingerprint.Compute(dir)
	if err != nil {
		return nil, err
	}
	out := make([]deploy.UploadFile, 0, len(fp.Files))
	for _, entry := range fp.Files {
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(entry.Path)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Path, err)
		}
		out = append(out, deploy.UploadFile{
			Path:          entry.Path,
			ContentBase64: base64.StdEncoding.EncodeToString(content),
		})
	}
	return out, nil
}
