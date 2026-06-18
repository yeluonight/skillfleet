// Package agentclient is the agent-side HTTP client for /agent/*
// routes that sit behind sfhmac signing (v1.0 §4.2). It hides the
// nonce minting, header installation, and JSON marshalling so the
// individual call sites (heartbeat, inventory, jobs, ...) read like
// plain function calls.
//
// The client is intentionally small: one struct, one Do method, plus
// the typed wrappers per route. It is NOT shared with the enrolment
// client — enrolment runs unsigned and writes to disk; signed calls
// only read in-memory config and have no side effects on the agent
// filesystem.
package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/sfhmac"
)

// Errors classify server-side rejections so callers can react
// without parsing the JSON envelope manually.
var (
	ErrUnauthorized      = errors.New("agentclient: unauthorized")
	ErrDeviceNotApproved = errors.New("agentclient: device not approved")
)

// Config holds the per-client wiring. ServerURL + DeviceID + Secret
// come from agentcfg.Config; Now is injectable for tests.
type Config struct {
	ServerURL    string
	DeviceID     string
	DeviceSecret string // plaintext as stored in agent.json
	HTTPClient   *http.Client
	Now          func() time.Time
}

// Client is the per-agent HTTP client. Zero value is not usable;
// construct with New.
type Client struct {
	cfg     Config
	hmacKey string // sha256(DeviceSecret), derived once
}

// New returns a Client ready to issue signed requests. It precomputes
// the HMAC key so each call avoids the sha256 round.
func New(cfg Config) (*Client, error) {
	if cfg.ServerURL == "" || cfg.DeviceID == "" || cfg.DeviceSecret == "" {
		return nil, errors.New("agentclient: server_url, device_id, device_secret required")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Client{
		cfg:     cfg,
		hmacKey: devices.HMACKey(cfg.DeviceSecret),
	}, nil
}

// do signs and sends one request. The request body (if any) is
// marshalled from `payload` (JSON); pass nil for a body-less request.
// The response body is decoded into `into` if non-nil. Status codes
// outside 2xx are returned as typed errors when recognised.
func (c *Client) do(ctx context.Context, method, path string, payload, into any) error {
	var body []byte
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("agentclient: marshal payload: %w", err)
		}
		body = raw
	}

	url := strings.TrimRight(c.cfg.ServerURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agentclient: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if err := sfhmac.SignRequest(req, c.cfg.DeviceID, c.hmacKey, "", c.cfg.Now(), body); err != nil {
		return fmt.Errorf("agentclient: sign: %w", err)
	}

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("agentclient: POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classifyError(resp)
	}
	if into != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(into); err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("agentclient: decode response: %w", err)
		}
	}
	return nil
}

// HeartbeatRequest is the payload of POST /agent/heartbeat. Agent
// version is informational and updated on the server only when this
// payload differs from the recorded value.
type HeartbeatRequest struct {
	AgentVersion string `json:"agent_version,omitempty"`
}

// HeartbeatResponse currently just carries status echo. The handler
// keeps room for future hints (e.g. "rotate secret", "pull jobs now").
type HeartbeatResponse struct {
	Status string `json:"status"`
}

// Heartbeat sends one heartbeat and returns the server's hints.
func (c *Client) Heartbeat(ctx context.Context, req HeartbeatRequest) (*HeartbeatResponse, error) {
	var resp HeartbeatResponse
	if err := c.do(ctx, http.MethodPost, "/agent/heartbeat", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// InventoryResponse echoes the server's run summary.
type InventoryResponse struct {
	Status     string `json:"status"`
	RunID      string `json:"run_id"`
	SkillCount int    `json:"skill_count"`
	RootCount  int    `json:"root_count"`
}

// Inventory submits a full skill-scan report. The payload is the
// shared inventory.Report (passed as any to avoid importing the
// inventory package here — the agent marshals it the same way the
// server unmarshals it).
func (c *Client) Inventory(ctx context.Context, report any) (*InventoryResponse, error) {
	var resp InventoryResponse
	if err := c.do(ctx, http.MethodPost, "/agent/inventory", report, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// classifyError translates a non-2xx response into one of the typed
// errors above when the server's `error` code is recognised. The raw
// body is preserved in the wrapping error for unknown codes.
func classifyError(resp *http.Response) error {
	var er struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4*1024)).Decode(&er)
	switch er.Error {
	case "device_not_approved":
		return fmt.Errorf("%w: %s", ErrDeviceNotApproved, er.Message)
	case "bad_signature", "timestamp_out_of_window", "nonce_replay", "body_mismatch":
		return fmt.Errorf("%w: %s (%s)", ErrUnauthorized, er.Error, er.Message)
	}
	if er.Message != "" {
		return fmt.Errorf("agentclient: %d: %s", resp.StatusCode, er.Message)
	}
	return fmt.Errorf("agentclient: %d: %s", resp.StatusCode, http.StatusText(resp.StatusCode))
}
