// Package api builds the HTTP handler tree served on the single
// configured port (v2.0 §5.6). Phase 1 surface:
//
//	GET  /health      → liveness, no auth
//	GET  /api/status  → public; reports whether setup is still needed
//	POST /api/setup   → consume the boot-time code; create first admin
//	POST /api/login   → password login; sets sf_session + sf_csrf
//	GET  /api/me      → returns the current user (auth required)
//	POST /api/logout  → revokes session + clears cookies (auth + CSRF)
//
// CSRF is enforced via the double-submit pattern (internal/csrf) on
// every /api write outside the bootstrap pair (setup / login), and
// every authenticated route runs under the session middleware which
// rejects callers without a valid sf_session cookie.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/auth"
	"github.com/yeluonight/skillfleet/internal/csrf"
	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/draft"
	"github.com/yeluonight/skillfleet/internal/ratelimit"
	"github.com/yeluonight/skillfleet/internal/registry"
	"github.com/yeluonight/skillfleet/internal/session"
	"github.com/yeluonight/skillfleet/internal/setup"
	"github.com/yeluonight/skillfleet/internal/source"
)

// Deps groups the runtime references each handler needs. Constructing
// the router via NewRouter keeps wiring centralised and makes the
// handlers easy to unit-test with httptest.
type Deps struct {
	DB         *sql.DB
	Logger     *slog.Logger
	Now        func() time.Time // injectable so tests don't drift on wall clock
	Audit      *audit.Logger
	SessionTTL time.Duration
	LoginIP    ratelimit.Rate // per-IP rate (e.g. "10/min" parsed)
	LoginUser  ratelimit.Rate // per-username rate
	// HTTPS toggles the cookie Secure attribute. Set true when the
	// external_url is HTTPS or when a TLS-terminating proxy will be
	// present in front of the server.
	HTTPS bool
	// WebUI, when non-nil, is mounted on `GET /` (catch-all) so the
	// embedded React bundle answers requests that aren't claimed by
	// /api, /agent, or /health. Tests typically leave it nil.
	WebUI http.Handler
	// Agent, when non-nil, is composed into the same mux so /agent/*
	// patterns it registers take precedence over the WebUI catch-all.
	// Phase 2 t5 introduces this with /agent/enroll; t7+ add the
	// HMAC-guarded routes (heartbeat, inventory, ...).
	Agent http.Handler
	// Registry, when non-nil, backs the /api/skills* routes (phase 4).
	// Tests that don't exercise the registry leave it nil; those
	// handlers then return 503 registry_unavailable.
	Registry *registry.Store
	// Drafts, when non-nil, backs the /api/skill-drafts* routes
	// (phase 4 t8+). Nil → those handlers return 503.
	Drafts *draft.Store
	// Sources, when non-nil, backs the source-binding routes
	// (phase 6 t4+: bind/detach/check-updates). Nil → 503.
	Sources *source.Store
	// Fetcher pulls skill content from a remote repo for bind/check
	// (phase 6 t3). It is an interface so tests inject a fake instead of
	// hitting the network; *source.Fetcher is the production impl. Nil →
	// the binding handlers return 503.
	Fetcher SourceFetcher
	// Deploy, when non-nil, backs the /api/deployments* routes (phase 8:
	// plan/execute/rollback/list). Nil → those handlers return 503.
	Deploy *deploy.Store
}

// SourceFetcher is the subset of *source.Fetcher the API layer needs.
// Defining it here (consumer side) lets handler tests inject a fake that
// returns canned trees instead of cloning a real repository.
type SourceFetcher interface {
	LsRemote(ctx context.Context, repoURL string, ref source.RemoteRef) (string, error)
	FetchSubdir(ctx context.Context, repoURL string, ref source.RemoteRef, subdir string) (source.FetchResult, error)
}

// NewRouter returns an http.Handler with all phase-1 routes wired.
func NewRouter(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	mux := http.NewServeMux()

	// Public, no auth, no CSRF.
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /api/status", d.handleStatus)
	mux.HandleFunc("POST /api/setup", d.handleSetup)

	// Login is the bootstrap that issues both cookies, so it cannot
	// itself require a CSRF cookie. SameSite=Strict on the eventual
	// session cookie + a JSON Content-Type check is the cross-site
	// defence here.
	loginIP := ratelimit.New(d.LoginIP, d.Now)
	loginUser := ratelimit.New(d.LoginUser, d.Now)
	dummy := newDummyHash()
	mux.Handle("POST /api/login", &loginHandler{deps: d, byIP: loginIP, byUser: loginUser, dummy: dummy})

	// Authenticated routes. requireAuth runs first to attach the session
	// to the request context; requireCSRF wraps writes (POST /api/logout).
	mux.Handle("GET /api/me", d.requireAuth(http.HandlerFunc(d.handleMe)))
	mux.Handle("POST /api/logout", d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleLogout))))

	// Enrolment token management (phase 2 t4). Both endpoints sit
	// behind auth; creates and revokes are writes so require CSRF.
	mux.Handle("GET /api/enrollment-tokens",
		d.requireAuth(http.HandlerFunc(d.handleListEnrollmentTokens)))
	mux.Handle("POST /api/enrollment-tokens",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleCreateEnrollmentToken))))
	mux.Handle("POST /api/enrollment-tokens/{id}/revoke",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleRevokeEnrollmentToken))))

	// Devices management (phase 2 t9). GET is auth-only; mutations
	// require CSRF. State-machine enforcement + audit rows live in
	// handleSetDeviceStatus.
	mux.Handle("GET /api/devices",
		d.requireAuth(http.HandlerFunc(d.handleListDevices)))
	mux.Handle("POST /api/devices/{id}/approve",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleApproveDevice))))
	mux.Handle("POST /api/devices/{id}/revoke",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleRevokeDevice))))
	mux.Handle("GET /api/devices/{id}/inventory",
		d.requireAuth(http.HandlerFunc(d.handleDeviceInventory)))
	mux.Handle("POST /api/devices/{id}/roots",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleRegisterDeviceRoot))))
	mux.Handle("POST /api/devices/{id}/roots/{rootId}/remove",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleRemoveDeviceRoot))))

	// Device drift (phase 7 t3, v1.0 §8.2): classify a device's latest
	// inventory run against the registry by content_sha256 — clean /
	// local_modified / untracked. Read-only (auth, no CSRF). {id} is the
	// device id. Returns 503 when the registry is not configured.
	mux.Handle("GET /api/devices/{id}/drift",
		d.requireAuth(http.HandlerFunc(d.handleDeviceDrift)))

	// Skills Registry (phase 4 t6). GET routes are auth-only; create is
	// a write so requires CSRF. The {id} path value is the skill name.
	mux.Handle("GET /api/skills",
		d.requireAuth(http.HandlerFunc(d.handleListSkills)))
	mux.Handle("POST /api/skills",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleCreateSkill))))
	mux.Handle("GET /api/skills/{id}",
		d.requireAuth(http.HandlerFunc(d.handleGetSkill)))

	// Source binding (phase 6 t4). Binding a skill to an upstream repo
	// triggers a network fetch + a baseline upstream version, so it is a
	// write (CSRF). The {id} path value is the skill name.
	mux.Handle("POST /api/skills/{id}/bind-source",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleBindSource))))

	// Binding-wizard dry run (phase 6 t8): fetch the upstream subdir and
	// report what WOULD be bound, writing nothing. It triggers an external
	// network fetch just like bind-source, so it is gated by auth + CSRF too
	// (a CSRF-driven probe of arbitrary URLs is exactly what we don't want).
	mux.Handle("POST /api/skills/{id}/bind-source/preview",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleBindSourcePreview))))

	// Source update check + detach (phase 6 t6). check-updates runs the
	// §8.4 engine (a network fetch + possible new version); detach removes
	// the binding. Both mutate, so CSRF applies. {id} is the skill name.
	mux.Handle("POST /api/skills/{id}/check-updates",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleCheckUpdates))))
	mux.Handle("POST /api/skills/{id}/detach-source",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleDetachSource))))

	// Capture local (phase 7 t6, §8.3): publish a device's locally-edited
	// copy as a new local_edit version. A write (auth + CSRF). The files
	// come from the caller in Phase 7 (agent auto-upload is Phase 8); the
	// registry write + base_version_id provenance is the finished part.
	// {id} is the skill name.
	mux.Handle("POST /api/skills/{id}/capture-local",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleCaptureLocal))))

	// Updates Page aggregate (phase 6 t9, v1.0 §13.7): skills grouped by the
	// six update dimensions + Dashboard summary counts. Read-only (auth, no
	// CSRF). Phase 6 fills only the upstream dimension.
	mux.Handle("GET /api/updates",
		d.requireAuth(http.HandlerFunc(d.handleListUpdates)))

	// Upstream diff (phase 6 t10, §17 task 6): two-way file-level comparison
	// between a bound skill's baseline upstream and its pending upstream
	// version. Read-only (auth, no CSRF). {id} is the skill name.
	mux.Handle("GET /api/skills/{id}/upstream-diff",
		d.requireAuth(http.HandlerFunc(d.handleUpstreamDiff)))

	// Three-way diff (phase 7 t5, §8.5): base | local | remote for a bound
	// skill. base/remote (registry upstream versions) diff at file level;
	// local is sha-only (the agent reports a fingerprint, not bytes — per-
	// file local diff is Phase 8). Query device_id/tool_key/scope locate
	// the local side. Read-only (auth, no CSRF). {id} is the skill name.
	mux.Handle("GET /api/skills/{id}/three-way-diff",
		d.requireAuth(http.HandlerFunc(d.handleThreeWayDiff)))

	// Deployments (phase 8, §14.1): plan a deploy (dry-run preview),
	// execute one (create a pending job the agent claims), roll one back
	// (enqueue a restore-from-backup job), and list jobs. The three writes
	// take CSRF; the list is read-only. plan/execute resolve a registry
	// version server-side; the agent does the filesystem work.
	mux.Handle("POST /api/deployments/plan",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleDeployPlan))))
	mux.Handle("POST /api/deployments/execute",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleDeployExecute))))
	mux.Handle("POST /api/deployments/state-change",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleDeployStateChange))))
	mux.Handle("POST /api/deployments/{id}/rollback",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleDeployRollback))))
	mux.Handle("GET /api/deployments",
		d.requireAuth(http.HandlerFunc(d.handleListDeployments)))

	// Skill version file access (phase 4 t7). The file tree comes from
	// the manifest; a single file's bytes are unpacked on demand. The
	// {path...} wildcard captures multi-segment package paths.
	mux.Handle("GET /api/skill-versions/{id}/files",
		d.requireAuth(http.HandlerFunc(d.handleVersionFiles)))
	mux.Handle("GET /api/skill-versions/{id}/files/{path...}",
		d.requireAuth(http.HandlerFunc(d.handleVersionFile)))

	// Skill drafts (phase 4 t8). Create is a write (CSRF); get is
	// auth-only. The {id} is the draft id.
	mux.Handle("POST /api/skill-drafts",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleCreateDraft))))
	mux.Handle("GET /api/skill-drafts/{id}",
		d.requireAuth(http.HandlerFunc(d.handleGetDraft)))

	// Draft file mutations (phase 4 t9). All writes; require CSRF. The
	// {path...} wildcard captures multi-segment package paths.
	mux.Handle("POST /api/skill-drafts/{id}/files/{path...}",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleCreateDraftFile))))
	mux.Handle("PUT /api/skill-drafts/{id}/files/{path...}",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleReplaceDraftFile))))
	mux.Handle("DELETE /api/skill-drafts/{id}/files/{path...}",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleDeleteDraftFile))))
	mux.Handle("DELETE /api/skill-drafts/{id}",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleDeleteDraft))))

	// Draft validate + publish (phase 4 t10). Both writes; require
	// CSRF. Publish forks a new immutable version and closes the draft.
	mux.Handle("POST /api/skill-drafts/{id}/validate",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handleValidateDraft))))
	mux.Handle("POST /api/skill-drafts/{id}/publish",
		d.requireAuth(d.requireCSRF(http.HandlerFunc(d.handlePublishDraft))))

	// WebUI catch-all. The Go 1.22 mux gives this pattern the lowest
	// precedence among handlers (the more specific /health,
	// /api/status, /api/me, and /agent/ patterns will win), so we
	// can mount the SPA at "/" without shadowing the API.
	// Using "/" (any method) rather than "GET /" sidesteps the
	// mux's "method-specific pattern conflicts with broader path
	// pattern" panic when /agent/ is also registered; the SPA
	// handler itself enforces GET/HEAD-only and returns 405 for
	// anything else.
	if d.WebUI != nil {
		mux.Handle("/", d.WebUI)
	}

	// /agent/* tree. Mounting via Handle("/agent/", ...) makes the
	// supplied handler responsible for the entire subtree; method-
	// aware patterns inside it ensure unmatched methods get 405
	// instead of being delegated up to the WebUI catch-all.
	if d.Agent != nil {
		mux.Handle("/agent/", d.Agent)
	}

	return mux
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := setup.CurrentStatus(r.Context(), d.DB)
	if err != nil {
		d.logErr("status: query", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"setup_required": st.Required,
	})
}

type setupRequest struct {
	Code     string `json:"code"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type setupResponse struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

func (d Deps) handleSetup(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeJSON[setupRequest](w, r, 4*1024)
	if !ok {
		return
	}

	uid, err := setup.Consume(r.Context(), d.DB, req.Code, req.Username, req.Password, d.Now())
	switch {
	case err == nil:
		d.logInfo("setup: admin created",
			slog.String("user_id", uid),
			slog.String("username", req.Username),
		)
		writeJSON(w, http.StatusCreated, setupResponse{UserID: uid, Username: req.Username})
	case errors.Is(err, setup.ErrAlreadyConsumed):
		writeError(w, http.StatusConflict, "already_consumed", "setup has already been completed")
	case errors.Is(err, setup.ErrNoPending):
		// No pending row almost always means "already consumed" from the
		// caller's perspective — but the two states are distinct in DB.
		writeError(w, http.StatusConflict, "no_pending_setup", "no pending setup; check server log for a fresh code")
	case errors.Is(err, setup.ErrCodeMismatch):
		writeError(w, http.StatusForbidden, "code_mismatch", "setup code does not match")
	default:
		// Validation errors (username/password) are user-fixable; treat
		// every non-sentinel error as a 400 with the (sanitised) message.
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	}
}

// loginHandler holds the resources login needs across requests:
// per-IP / per-user limiters and a lazily-initialised "dummy" argon2
// hash used to neutralise the timing difference between known and
// unknown usernames.
type loginHandler struct {
	deps   Deps
	byIP   *ratelimit.Limiter
	byUser *ratelimit.Limiter
	dummy  *dummyHash
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	ExpiresAt int64  `json:"expires_at"` // unix millis
}

func (h *loginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, ok := decodeJSON[loginRequest](w, r, 4*1024)
	if !ok {
		return
	}

	ip := clientIP(r)
	username := strings.TrimSpace(req.Username)

	// IP limiter is checked before the username limiter so a single IP
	// hammering many usernames trips on its own counter first.
	if ok, wait := h.byIP.Allow(ip); !ok {
		h.deps.recordLogin(ctx, "auth.login.rate_limited", "", username, ip, "ip")
		writeRateLimited(w, wait)
		return
	}
	// Lowercase the username key so "Alice" and "alice" share the bucket;
	// they refer to the same user from the DB UNIQUE constraint's POV.
	if ok, wait := h.byUser.Allow(strings.ToLower(username)); !ok {
		h.deps.recordLogin(ctx, "auth.login.rate_limited", "", username, ip, "user")
		writeRateLimited(w, wait)
		return
	}

	// Always run argon2 verification (against a dummy hash when the user
	// doesn't exist) so the timing is indistinguishable between
	// "unknown user" and "wrong password". Combine the membership and
	// match flags with constant-time AND so the branch below doesn't
	// leak either signal individually.
	var (
		userID, storedHash string
		userFound          bool
	)
	err := h.deps.DB.QueryRowContext(ctx,
		`SELECT id, password_hash FROM users WHERE username = ?`, username,
	).Scan(&userID, &storedHash)
	switch {
	case err == nil:
		userFound = true
	case errors.Is(err, sql.ErrNoRows):
		userFound = false
	default:
		h.deps.logErr("login: user lookup", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	hashForCheck := storedHash
	if !userFound {
		hashForCheck = h.dummy.encoded()
	}
	verifyErr := auth.VerifyPassword(hashForCheck, req.Password)

	// Use constant-time AND of (found ? 1 : 0) and (verifyErr==nil ? 1 : 0).
	foundByte := byte(0)
	if userFound {
		foundByte = 1
	}
	matchByte := byte(0)
	if verifyErr == nil {
		matchByte = 1
	}
	authed := subtle.ConstantTimeByteEq(foundByte&matchByte, 1) == 1

	if !authed {
		h.deps.recordLogin(ctx, "auth.login.failure", userID, username, ip, "")
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}

	sess, token, err := session.Create(ctx, h.deps.DB, userID, ip, r.UserAgent(), h.deps.SessionTTL, h.deps.Now())
	if err != nil {
		h.deps.logErr("login: session create", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     session.CookieName,
		Value:    token,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		MaxAge:   int(h.deps.SessionTTL / time.Second),
		HttpOnly: true,
		Secure:   h.deps.HTTPS,
		SameSite: http.SameSiteStrictMode,
	})

	// Pair the session with a CSRF cookie so subsequent writes can
	// pass the double-submit check. A new login rotates the token —
	// any reused tab will refresh on /api/me and pick the fresh value
	// out of document.cookie.
	csrfToken, err := csrf.NewToken()
	if err != nil {
		h.deps.logErr("login: csrf token", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	csrf.SetCookie(w, csrfToken, h.deps.HTTPS)

	h.deps.recordLogin(ctx, "auth.login.success", userID, username, ip, "")
	writeJSON(w, http.StatusOK, loginResponse{
		UserID:    userID,
		Username:  username,
		ExpiresAt: sess.ExpiresAt.UnixMilli(),
	})
}

// recordLogin writes one audit row. Failure to audit must never abort
// the login itself — Logger.Write swallows DB errors.
func (d Deps) recordLogin(ctx context.Context, action, userID, username, ip, limiter string) {
	if d.Audit == nil {
		return
	}
	detail := map[string]any{
		"ip":       ip,
		"username": username,
	}
	if limiter != "" {
		detail["limiter"] = limiter
	}
	d.Audit.Write(ctx, audit.Record{
		Actor:  audit.Actor{Type: "user", ID: userID},
		Action: action,
		Detail: detail,
	})
}

// clientIP extracts the best-effort client IP from a request. The
// reverse proxy guidance in v2.0 §5.6 expects a single deployment of
// the server behind one trusted proxy, so X-Forwarded-For's first
// element is acceptable when present; we still fall back to
// RemoteAddr otherwise.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the client, subsequent entries are
		// intermediate proxies. Take only the first.
		if comma := strings.IndexByte(xff, ','); comma >= 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(retryAfter.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("too many attempts; retry after %ds", secs))
}

// dummyHash is a lazily-computed argon2id hash of a random string.
// VerifyPassword always runs against a real hash so the wall-clock cost
// of a login attempt does not reveal whether the username exists.
type dummyHash struct {
	once     sync.Once
	encoded_ string
}

func newDummyHash() *dummyHash { return &dummyHash{} }

func (d *dummyHash) encoded() string {
	d.once.Do(func() {
		var buf [64]byte
		_, _ = rand.Read(buf[:]) // crypto/rand.Read never fails on Linux
		// HashPassword can only fail on empty password, which is not
		// our case here. Treat the error as impossible and fall back
		// to a hardcoded structurally-valid encoded hash if it ever
		// happens — VerifyPassword will run argon2 either way.
		if h, err := auth.HashPassword(string(buf[:]) + "_dummy_input"); err == nil {
			d.encoded_ = h
			return
		}
		d.encoded_ = "$argon2id$v=19$m=65536,t=3,p=2$" +
			"YWFhYWFhYWFhYWFhYWFhYQ$" +
			"YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE"
	})
	return d.encoded_
}

// --- middleware ---

// ctxKey is a private type for stashing values on the request context
// so callers cannot collide with our keys.
type ctxKey int

const ctxKeySession ctxKey = iota

// SessionFromContext returns the session attached by requireAuth.
// Handlers behind requireAuth can rely on the second return value
// being true.
func SessionFromContext(ctx context.Context) (session.Session, bool) {
	s, ok := ctx.Value(ctxKeySession).(session.Session)
	return s, ok
}

// requireAuth rejects requests without a live session cookie. On
// success it stores the resolved Session on the request context and
// transparently refreshes a missing sf_csrf cookie so newly-restored
// tabs can immediately POST.
func (d Deps) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(session.CookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "session required")
			return
		}
		sess, err := session.Lookup(r.Context(), d.DB, cookie.Value, d.Now())
		if err != nil {
			// Any lookup failure (not found, expired, revoked) yields
			// the same client-visible state: unauthenticated. The
			// distinction stays in the audit log if we ever choose to
			// emit one here.
			writeError(w, http.StatusUnauthorized, "unauthenticated", "session invalid or expired")
			return
		}
		if _, err := r.Cookie(csrf.CookieName); err != nil {
			// Best-effort top-up: a brand-new login won the previous
			// roundtrip, but a tab restored from cold storage may
			// have lost only the (non-HttpOnly) csrf cookie. Mint a
			// fresh one rather than forcing a re-login.
			if tok, terr := csrf.NewToken(); terr == nil {
				csrf.SetCookie(w, tok, d.HTTPS)
			}
		}
		ctx := context.WithValue(r.Context(), ctxKeySession, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireCSRF enforces the double-submit cookie check on the wrapped
// handler. The handler is only invoked when the cookie and header
// match. requireCSRF MUST run inside requireAuth so unauthenticated
// callers get a 401 (clearer signal) rather than a 403 from a missing
// CSRF cookie.
func (d Deps) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch err := csrf.Verify(r); {
		case err == nil:
			next.ServeHTTP(w, r)
		case errors.Is(err, csrf.ErrMissing):
			writeError(w, http.StatusForbidden, "csrf_missing", "CSRF token missing")
		default:
			writeError(w, http.StatusForbidden, "csrf_mismatch", "CSRF token mismatch")
		}
	})
}

// --- /api/me, /api/logout ---

type meResponse struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	ExpiresAt int64  `json:"expires_at"`
}

func (d Deps) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		// requireAuth guarantees this branch is unreachable.
		writeError(w, http.StatusInternalServerError, "internal_error", "missing session in context")
		return
	}
	var username string
	if err := d.DB.QueryRowContext(r.Context(),
		`SELECT username FROM users WHERE id = ?`, sess.UserID,
	).Scan(&username); err != nil {
		// Race: user row was deleted between session creation and now.
		// Treat as unauthenticated and revoke the dangling session.
		d.logErr("me: user lookup", err)
		_ = session.Revoke(r.Context(), d.DB, sess.ID, d.Now())
		writeError(w, http.StatusUnauthorized, "unauthenticated", "user no longer exists")
		return
	}
	writeJSON(w, http.StatusOK, meResponse{
		UserID:    sess.UserID,
		Username:  username,
		ExpiresAt: sess.ExpiresAt.UnixMilli(),
	})
}

func (d Deps) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "missing session in context")
		return
	}
	if err := session.Revoke(r.Context(), d.DB, sess.ID, d.Now()); err != nil {
		d.logErr("logout: revoke", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	// Tell the browser to forget both cookies. MaxAge=-1 is a delete.
	http.SetCookie(w, &http.Cookie{
		Name: session.CookieName, Value: "", Path: "/",
		MaxAge: -1, HttpOnly: true, Secure: d.HTTPS, SameSite: http.SameSiteStrictMode,
	})
	csrf.ClearCookie(w, d.HTTPS)

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  audit.Actor{Type: "user", ID: sess.UserID},
			Action: "auth.logout",
			Target: audit.Target{Type: "session", ID: sess.ID},
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---

type apiError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiError{Error: code, Message: msg})
}

// decodeOption customises decodeJSON for the few handlers that deviate
// from the common path (Content-Type enforcement + a bare 400 message).
type decodeOption func(*decodeConfig)

type decodeConfig struct {
	skipContentType bool
	detailError     bool
}

// skipContentTypeCheck tells decodeJSON not to enforce an
// "application/json" Content-Type. Used by the deploy handlers (which
// have never required it) and by handleCreateSkill (which routes
// zip-vs-json itself before decoding).
func skipContentTypeCheck() decodeOption {
	return func(c *decodeConfig) { c.skipContentType = true }
}

// withDecodeErrorDetail appends the decoder's error text to the 400
// message ("invalid JSON body: <detail>"), preserving the deploy
// handlers' pre-refactor diagnostic.
func withDecodeErrorDetail() decodeOption {
	return func(c *decodeConfig) { c.detailError = true }
}

// decodeJSON size-limits and strictly decodes a JSON request body into a
// T. On any failure it writes the matching 4xx error (415 for a non-JSON
// Content-Type, 400 for a malformed/over-limit body) and returns
// (zero, false) so the caller can return immediately. maxBytes caps the
// body via http.MaxBytesReader.
func decodeJSON[T any](w http.ResponseWriter, r *http.Request, maxBytes int64, opts ...decodeOption) (T, bool) {
	var cfg decodeConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	var body T
	if !cfg.skipContentType && !hasJSONContentType(r) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
		return body, false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		msg := "invalid JSON body"
		if cfg.detailError {
			msg += ": " + err.Error()
		}
		writeError(w, http.StatusBadRequest, "bad_request", msg)
		return body, false
	}
	return body, true
}

func (d Deps) logErr(msg string, err error) {
	if d.Logger == nil {
		return
	}
	d.Logger.Error(msg, slog.String("err", err.Error()))
}

func (d Deps) logInfo(msg string, attrs ...any) {
	if d.Logger == nil {
		return
	}
	d.Logger.Info(msg, attrs...)
}
