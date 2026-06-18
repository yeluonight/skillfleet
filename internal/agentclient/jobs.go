// agentclient — jobs.go: the downlink calls (claim a job, download its
// package, report the result). They sit behind the same sfhmac signing
// as heartbeat/inventory; Jobs and DownloadPackage are written directly
// (not via do) because one distinguishes 200 vs 204 and the other
// streams a body rather than decoding JSON.
package agentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/sfhmac"
)

// Jobs claims the next pending job for this device. ok is false (with a
// nil error) when the server has no work (HTTP 204). A signed GET with
// an empty body; the server returns the claimed job or 204. The response
// shape is deploy.ClaimedJob, shared with the server (agentapi).
func (c *Client) Jobs(ctx context.Context) (job deploy.ClaimedJob, ok bool, err error) {
	req, err := c.signedGet(ctx, "/agent/jobs")
	if err != nil {
		return deploy.ClaimedJob{}, false, err
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return deploy.ClaimedJob{}, false, fmt.Errorf("agentclient: GET /agent/jobs: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNoContent:
		return deploy.ClaimedJob{}, false, nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&job); err != nil {
			return deploy.ClaimedJob{}, false, fmt.Errorf("agentclient: decode job: %w", err)
		}
		return job, true, nil
	default:
		return deploy.ClaimedJob{}, false, classifyError(resp)
	}
}

// DownloadPackage fetches a package archive stream from downloadPath
// (e.g. "/agent/packages/sv_xxx"). The caller MUST Close the returned
// reader. The body is whatever the server streams; size/integrity are
// the caller's responsibility (agentinstall.DownloadVerified hashes it).
func (c *Client) DownloadPackage(ctx context.Context, downloadPath string) (io.ReadCloser, error) {
	req, err := c.signedGet(ctx, downloadPath)
	if err != nil {
		return nil, err
	}
	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentclient: GET %s: %w", downloadPath, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer func() { _ = resp.Body.Close() }()
		return nil, classifyError(resp)
	}
	return resp.Body, nil
}

// JobResult reports a job's terminal outcome. It is a signed POST; the
// server records the result and settles the job. The request body shape
// is deploy.JobResult, shared with the server (agentapi).
func (c *Client) JobResult(ctx context.Context, jobID string, req deploy.JobResult) error {
	return c.do(ctx, http.MethodPost, "/agent/jobs/"+jobID+"/result", req, nil)
}

// UploadSkill POSTs a captured skill's files to /agent/upload so the server
// adopts them into the registry (mgmt-refactor track A). Signed like the
// other downlink calls; the server raises the body cap for this route. The
// reply carries the new registry version id.
func (c *Client) UploadSkill(ctx context.Context, req deploy.UploadRequest) (deploy.UploadResponse, error) {
	var resp deploy.UploadResponse
	if err := c.do(ctx, http.MethodPost, "/agent/upload", req, &resp); err != nil {
		return deploy.UploadResponse{}, err
	}
	return resp, nil
}

// signedGet builds a body-less, HMAC-signed GET. The empty body hashes
// to sha256("") which the server's middleware accepts; SignRequest
// handles a nil body.
func (c *Client) signedGet(ctx context.Context, path string) (*http.Request, error) {
	url := strings.TrimRight(c.cfg.ServerURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("agentclient: build GET %s: %w", path, err)
	}
	if err := sfhmac.SignRequest(req, c.cfg.DeviceID, c.hmacKey, "", c.cfg.Now(), nil); err != nil {
		return nil, fmt.Errorf("agentclient: sign GET %s: %w", path, err)
	}
	return req, nil
}
