package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/enrollment"
)

// enrollmentTokenView is the JSON shape returned by GET /api/enrollment-tokens.
// Plaintext is never present — Create is the only place that ever
// echoes the raw token, and only into the immediate response.
type enrollmentTokenView struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`         // unix millis
	ExpiresAt int64  `json:"expires_at"`         // unix millis
	UsedAt    *int64 `json:"used_at,omitempty"`  // present only when used
}

// createEnrollmentTokenResponse is the one-shot payload from POST
// /api/enrollment-tokens. `token` is the plaintext to display in the
// WebUI banner; the operator copies it into the agent CLI.
type createEnrollmentTokenResponse struct {
	ID        string `json:"id"`
	Token     string `json:"token"`        // plaintext, returned once
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

func (d Deps) handleCreateEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "missing session in context")
		return
	}
	// TTL is fixed for now. A future task can let the operator pick
	// from a short menu (10m / 1h / 24h) if there is a real need.
	tok, err := enrollment.Create(r.Context(), d.DB, enrollment.DefaultTTL, d.Now())
	if err != nil {
		d.logErr("enrollment-tokens: create", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  audit.Actor{Type: "user", ID: sess.UserID},
			Action: "enrollment.token.created",
			Target: audit.Target{Type: "enrollment_token", ID: tok.ID},
			Detail: map[string]any{"expires_at": tok.ExpiresAt.UnixMilli()},
		})
	}

	writeJSON(w, http.StatusCreated, createEnrollmentTokenResponse{
		ID:        tok.ID,
		Token:     tok.Plaintext,
		Status:    tok.Status,
		CreatedAt: tok.CreatedAt.UnixMilli(),
		ExpiresAt: tok.ExpiresAt.UnixMilli(),
	})
}

func (d Deps) handleListEnrollmentTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := enrollment.List(r.Context(), d.DB, 100)
	if err != nil {
		d.logErr("enrollment-tokens: list", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	out := make([]enrollmentTokenView, 0, len(tokens))
	for _, t := range tokens {
		v := enrollmentTokenView{
			ID:        t.ID,
			Status:    t.Status,
			CreatedAt: t.CreatedAt.UnixMilli(),
			ExpiresAt: t.ExpiresAt.UnixMilli(),
		}
		if !t.UsedAt.IsZero() {
			ms := t.UsedAt.UnixMilli()
			v.UsedAt = &ms
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (d Deps) handleRevokeEnrollmentToken(w http.ResponseWriter, r *http.Request) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "missing session in context")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing token id")
		return
	}

	switch err := enrollment.Revoke(r.Context(), d.DB, id, d.Now()); {
	case err == nil:
		if d.Audit != nil {
			d.Audit.Write(r.Context(), audit.Record{
				Actor:  audit.Actor{Type: "user", ID: sess.UserID},
				Action: "enrollment.token.revoked",
				Target: audit.Target{Type: "enrollment_token", ID: id},
			})
		}
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, enrollment.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "enrollment token not found")
	default:
		// Wrong status (already used) -> 409. Other errors -> 500.
		if strings.Contains(err.Error(), "cannot revoke") {
			writeError(w, http.StatusConflict, "wrong_status", err.Error())
			return
		}
		d.logErr("enrollment-tokens: revoke", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}
