// /agent/inventory: the agent's full skill-scan snapshot. The HMAC
// middleware has already authenticated the device, verified the body
// hash, and enforced the nonce/time-window guards; this handler only
// has to decode the (potentially large) JSON report and hand it to
// inventory.Store, which replaces the device's prior inventory inside
// one transaction.
//
// Unlike heartbeat, a body is REQUIRED — an empty inventory is still a
// JSON object with an empty tools array, not a zero-length body.

package agentapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yeluonight/skillfleet/internal/audit"
	"github.com/yeluonight/skillfleet/internal/inventory"
)

type inventoryResponse struct {
	Status     string `json:"status"`
	RunID      string `json:"run_id"`
	SkillCount int    `json:"skill_count"`
	RootCount  int    `json:"root_count"`
}

func (d Deps) handleInventory(w http.ResponseWriter, r *http.Request) {
	ac, ok := FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal_error", "missing auth context")
		return
	}

	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "expected application/json")
		return
	}

	// The middleware already capped + buffered the body; decode from
	// the restored r.Body. DisallowUnknownFields so a schema drift on
	// the agent side surfaces loudly rather than silently dropping data.
	var report inventory.Report
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&report); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid inventory JSON: "+err.Error())
		return
	}

	res, err := inventory.Store(r.Context(), d.DB, ac.DeviceID, report, d.Now())
	if err != nil {
		// Validation errors are client-fixable (bad scope / state /
		// empty name); everything else is a server fault.
		if errors.Is(err, inventory.ErrInvalidReport) {
			writeError(w, http.StatusBadRequest, "invalid_inventory", err.Error())
			return
		}
		d.logErr("inventory: store", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	if d.Audit != nil {
		d.Audit.Write(r.Context(), audit.Record{
			Actor:  audit.Actor{Type: "device", ID: ac.DeviceID},
			Action: "device.inventory",
			Target: audit.Target{Type: "device", ID: ac.DeviceID},
			Detail: map[string]any{
				"run_id":      res.RunID,
				"skill_count": res.SkillCount,
				"root_count":  res.RootCount,
			},
		})
	}

	writeJSON(w, http.StatusOK, inventoryResponse{
		Status:     "ok",
		RunID:      res.RunID,
		SkillCount: res.SkillCount,
		RootCount:  res.RootCount,
	})
}
