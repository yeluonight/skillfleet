package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/devices"
)

// deviceView is the JSON shape returned by GET /api/devices. last_seen
// is omitted when the agent has never reported so the WebUI can
// distinguish "never online" from "0 ms since epoch".
type deviceView struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Hostname     string  `json:"hostname,omitempty"`
	OS           string  `json:"os,omitempty"`
	Arch         string  `json:"arch,omitempty"`
	AgentVersion string  `json:"agent_version,omitempty"`
	Status       string  `json:"status"`
	CreatedAt    int64   `json:"created_at"`
	LastSeenAt   *int64  `json:"last_seen_at,omitempty"`
}

func (d Deps) handleListDevices(w http.ResponseWriter, r *http.Request) {
	rows, err := devices.List(r.Context(), d.DB, 200)
	if err != nil {
		d.logErr("devices: list", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	out := make([]deviceView, 0, len(rows))
	for _, dev := range rows {
		v := deviceView{
			ID:           dev.ID,
			Name:         dev.Name,
			Hostname:     dev.Hostname,
			OS:           dev.OS,
			Arch:         dev.Arch,
			AgentVersion: dev.AgentVersion,
			Status:       dev.Status,
			CreatedAt:    dev.CreatedAt.UnixMilli(),
		}
		if !dev.LastSeenAt.IsZero() {
			ms := dev.LastSeenAt.UnixMilli()
			v.LastSeenAt = &ms
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

// handleApproveDevice flips pending → approved. Idempotent on the
// already-approved state (returns 200 with the current row) so a
// retried button click doesn't surface a confusing 409.
func (d Deps) handleApproveDevice(w http.ResponseWriter, r *http.Request) {
	d.handleSetDeviceStatus(w, r, devices.StatusApproved, "device.approved")
}

// handleRevokeDevice flips pending/approved → revoked. Same
// idempotence rule applies.
func (d Deps) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	d.handleSetDeviceStatus(w, r, devices.StatusRevoked, "device.revoked")
}

func (d Deps) handleSetDeviceStatus(w http.ResponseWriter, r *http.Request, want, action string) {
	sess, ok := SessionFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "missing session in context")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "missing device id")
		return
	}

	// Fetch first so we can short-circuit idempotent re-clicks and
	// emit a clean 404 for unknown devices.
	dev, err := devices.Get(r.Context(), d.DB, id)
	if err != nil {
		if errors.Is(err, devices.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "device not found")
			return
		}
		d.logErr("devices: get", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	if dev.Status == want {
		// Idempotent: nothing to do, no audit row.
		writeJSON(w, http.StatusOK, deviceFromRow(dev))
		return
	}

	if err := devices.SetStatus(r.Context(), d.DB, id, want); err != nil {
		if errors.Is(err, devices.ErrInvalidStatus) {
			// e.g. revoked → approved isn't allowed by the state machine.
			writeError(w, http.StatusConflict, "wrong_status",
				"cannot move device from "+dev.Status+" to "+want)
			return
		}
		d.logErr("devices: set status", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  audit.Actor{Type: "user", ID: sess.UserID},
			Action: action,
			Target: audit.Target{Type: "device", ID: id},
			Detail: map[string]any{
				"from": dev.Status,
				"to":   want,
				"name": dev.Name,
			},
		})
	}

	// Return the updated row so the WebUI can update without a separate GET.
	updated := dev
	updated.Status = want
	writeJSON(w, http.StatusOK, deviceFromRow(updated))
}

// deviceFromRow projects an internal devices.Device into the public
// JSON view. Mirrors the shape in handleListDevices.
func deviceFromRow(dev devices.Device) deviceView {
	v := deviceView{
		ID:           dev.ID,
		Name:         dev.Name,
		Hostname:     dev.Hostname,
		OS:           dev.OS,
		Arch:         dev.Arch,
		AgentVersion: dev.AgentVersion,
		Status:       dev.Status,
		CreatedAt:    dev.CreatedAt.UnixMilli(),
	}
	if !dev.LastSeenAt.IsZero() {
		ms := dev.LastSeenAt.UnixMilli()
		v.LastSeenAt = &ms
	}
	return v
}
