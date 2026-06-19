// Package enrollclient performs the agent side of the enrolment
// handshake (v1.0 §14.2): POST /agent/enroll with the operator-supplied
// token + machine metadata, then persist the resulting device_id /
// device_secret pair to agent.json.
//
// The package is split out of apps/agent so the HTTP + filesystem
// interactions are testable without driving the whole binary. The
// `enroll` subcommand in apps/agent/main.go is a thin shell around
// Run().
package enrollclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/yeluonight/skillfleet/internal/agentcfg"
)

// DefaultTimeout caps the round-trip; enrolment is a single small POST
// against a server the operator should already trust, so a tight bound
// makes the failure mode (operator typo'd URL) loud instead of hanging.
const DefaultTimeout = 15 * time.Second

// Options are the inputs the caller provides. ServerURL + Token + Name
// are required; the rest are auto-detected when omitted.
type Options struct {
	ServerURL    string // e.g. "https://sf.example.com" — trailing slash tolerated
	Token        string // enrolment token (sfen_*)
	Name         string // operator-supplied device name, required
	Hostname     string // defaults to os.Hostname()
	OS           string // defaults to runtime.GOOS
	Arch         string // defaults to runtime.GOARCH
	AgentVersion string // defaults to "dev" — apps/agent passes the build version
	ConfigPath   string // defaults to agentcfg.DefaultPath
	HTTPClient   *http.Client
}

// Result reports what was written so the CLI can echo it to stderr.
type Result struct {
	DeviceID   string
	Status     string
	ConfigPath string // expanded path that was actually written
}

// Sentinel errors so the CLI (and tests) can branch on outcome.
var (
	ErrTokenNotFound  = errors.New("enroll: token not recognised by server")
	ErrTokenExpired   = errors.New("enroll: token expired")
	ErrTokenNotUsable = errors.New("enroll: token already used or revoked")
	ErrAlreadyExists  = errors.New("enroll: agent.json already exists")
)

// enrollResponse mirrors agentapi's response shape. Duplicated to keep
// the agent binary free of server-side imports.
type enrollResponse struct {
	DeviceID     string `json:"device_id"`
	DeviceSecret string `json:"device_secret"`
	Status       string `json:"status"`
}

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Run executes the enrolment. The agent.json existence check runs
// BEFORE the HTTP call so a misconfigured re-run doesn't consume a
// fresh token; agentcfg.Save still uses O_EXCL as a belt-and-braces
// second check at write time.
func Run(ctx context.Context, opts Options) (Result, error) {
	if err := fillDefaults(&opts); err != nil {
		return Result{}, err
	}

	// Pre-flight: refuse if agent.json already exists. The server has
	// no way to undo a "used" token, so we must fail before POSTing.
	expanded, err := agentcfg.ExpandHome(opts.ConfigPath)
	if err != nil {
		return Result{}, fmt.Errorf("enroll: resolve config path: %w", err)
	}
	if _, err := os.Stat(expanded); err == nil {
		return Result{}, fmt.Errorf("%w: %s", ErrAlreadyExists, expanded)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, fmt.Errorf("enroll: stat %s: %w", expanded, err)
	}

	endpoint, err := buildEnrollURL(opts.ServerURL)
	if err != nil {
		return Result{}, err
	}

	body, err := json.Marshal(map[string]string{
		"token":         opts.Token,
		"name":          opts.Name,
		"hostname":      opts.Hostname,
		"os":            opts.OS,
		"arch":          opts.Arch,
		"agent_version": opts.AgentVersion,
	})
	if err != nil {
		return Result{}, fmt.Errorf("enroll: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("enroll: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("enroll: POST %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return Result{}, classifyError(resp)
	}

	var er enrollResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8*1024)).Decode(&er); err != nil {
		return Result{}, fmt.Errorf("enroll: decode response: %w", err)
	}
	if er.DeviceID == "" || er.DeviceSecret == "" {
		return Result{}, errors.New("enroll: server returned empty device_id or device_secret")
	}

	cfg := agentcfg.Config{
		ServerURL:       canonicalServerURL(opts.ServerURL),
		DeviceID:        er.DeviceID,
		DeviceSecret:    er.DeviceSecret,
		HeartbeatIntSec: agentcfg.DefaultHeartbeatSec,
		InventoryIntSec: agentcfg.DefaultInventorySec,
		JobsIntSec:      agentcfg.DefaultJobsSec,
	}
	if err := agentcfg.Save(opts.ConfigPath, cfg); err != nil {
		// Save uses O_EXCL too; if a race created the file between our
		// pre-flight stat and Save, surface ErrAlreadyExists.
		if strings.Contains(err.Error(), "refusing to overwrite") {
			return Result{}, fmt.Errorf("%w: %s", ErrAlreadyExists, expanded)
		}
		return Result{}, err
	}

	return Result{
		DeviceID:   er.DeviceID,
		Status:     er.Status,
		ConfigPath: expanded,
	}, nil
}

// fillDefaults populates auto-detected fields and validates required
// ones. It mutates opts in place.
func fillDefaults(opts *Options) error {
	opts.ServerURL = strings.TrimSpace(opts.ServerURL)
	opts.Token = strings.TrimSpace(opts.Token)
	opts.Name = strings.TrimSpace(opts.Name)
	if opts.ServerURL == "" {
		return errors.New("enroll: server URL required")
	}
	if opts.Token == "" {
		return errors.New("enroll: token required")
	}
	if opts.Name == "" {
		return errors.New("enroll: device name required (use -name)")
	}
	if opts.Hostname == "" {
		// os.Hostname's empty / error case is fine — server treats the
		// field as optional metadata.
		if h, err := os.Hostname(); err == nil {
			opts.Hostname = h
		}
	}
	if opts.OS == "" {
		opts.OS = runtime.GOOS
	}
	if opts.Arch == "" {
		opts.Arch = runtime.GOARCH
	}
	if opts.AgentVersion == "" {
		opts.AgentVersion = "dev"
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = agentcfg.DefaultPath
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: DefaultTimeout}
	}
	return nil
}

// buildEnrollURL joins serverURL + /agent/enroll, tolerating trailing
// slashes and rejecting schemes other than http/https.
func buildEnrollURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("enroll: parse server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("enroll: server URL must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("enroll: server URL missing host: %s", serverURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/agent/enroll"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// canonicalServerURL strips trailing slashes from the URL we persist
// so future requests build clean paths.
func canonicalServerURL(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil {
		return strings.TrimRight(serverURL, "/")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// classifyError reads the (small) error body and maps the server's
// error code to a typed sentinel where possible. The raw message is
// preserved so unexpected codes still surface diagnostically.
func classifyError(resp *http.Response) error {
	var er errorResponse
	if resp.Body != nil {
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4*1024)).Decode(&er)
	}
	switch er.Error {
	case "token_not_found":
		return ErrTokenNotFound
	case "token_expired":
		return ErrTokenExpired
	case "token_not_usable":
		return ErrTokenNotUsable
	}
	msg := er.Message
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return fmt.Errorf("enroll: server returned %d: %s", resp.StatusCode, msg)
}
