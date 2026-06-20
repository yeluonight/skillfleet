package api

import (
	"net/http"
	"time"

	"github.com/yeluonight/skillfleet/internal/deploy"
	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/drift"
)

// dashboard_handlers.go serves the Dashboard Page (§13.8.2): six headline
// metric cards plus the Top Action Items list. Read-only (auth, no CSRF). It
// is a pure aggregation over data the other endpoints already own — devices,
// the registry, the §13.7 update dimensions, and deployment jobs — so it adds
// no new table and never mutates.

// onlineWindow is how recently an approved device must have been seen to count
// as "online". The default agent heartbeat is 30s (agentcfg.DefaultHeartbeatSec);
// five minutes is ~10 missed beats — long enough to ride out a transient
// network blip or a sleeping laptop's clock skew, short enough that a genuinely
// dead agent drops off the count within a few minutes.
const onlineWindow = 5 * time.Minute

// failedJobScanLimit bounds the failed-deployment count query. deploy.List has
// no COUNT variant, so we page a generous slice and count the rows; a fleet
// with more than this many *failed* (terminal, not auto-cleared) jobs is well
// past the point where the exact headline number matters.
const failedJobScanLimit = 1000

// dashboardMetrics are the six headline counts (§13.8.2 layer 1). Each maps to
// one card the UI can click through to the relevant page.
type dashboardMetrics struct {
	OnlineDevices     int `json:"online_devices"`
	ManagedSkills     int `json:"managed_skills"`
	LocalEdits        int `json:"local_edits"`
	UpstreamUpdates   int `json:"upstream_updates"`
	FailedDeployments int `json:"failed_deployments"`
	HighRiskItems     int `json:"high_risk_items"`
	PendingDevices    int `json:"pending_devices"`
	UntrackedSkills   int `json:"untracked_skills"`
}

// dashboardActionItem is one entry in the Top Action Items list (§13.8.2
// layer 2). Key names the action class the UI routes on (e.g. "approve_devices"
// → Devices page); Count is how many items await; Label is the pre-localised
// fallback the server supplies (the UI prefers its own i18n string keyed by
// Key, falling back to this when absent).
type dashboardActionItem struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
	Label string `json:"label"`
}

// dashboardResponse is the GET /api/dashboard payload.
type dashboardResponse struct {
	Metrics     dashboardMetrics      `json:"metrics"`
	ActionItems []dashboardActionItem `json:"action_items"`
}

// handleDashboard serves GET /api/dashboard. Read-only (auth, no CSRF).
//
// The metrics come from four sources, each already proven by an existing
// endpoint:
//   - devices.List      → online (approved + seen within onlineWindow) / pending
//   - registry.ListSkills → managed skills count
//   - aggregateUpdates  → upstream_updates + local_edits (the §13.7 algorithm)
//   - deploy.List       → failed deployment jobs
//   - device drift      → untracked skills (registry knows no version of that name)
//
// "High-risk items" is a deliberately simple, explainable composite —
// untracked skills + (local edit AND upstream update conflicts) + failed
// deployments — drawn entirely from the aggregations above, no new table. It
// is the count the Risk Radar (§13.8 layer 3) elaborates; the headline card
// just sums the three contributing risks so an operator sees one number to act on.
//
// Requires the registry + sources to be wired (same guard as Updates); without
// them the page cannot compute its skill-centric metrics.
func (d Deps) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistryAndSources(w) {
		return
	}

	// Devices: online = approved AND last seen within the window; also count
	// pending (awaiting approval) for the action list.
	devs, err := devices.List(r.Context(), d.DB, 0)
	if err != nil {
		d.logErr("dashboard: list devices", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	now := d.Now()
	var online, pending int
	for _, dev := range devs {
		switch dev.Status {
		case devices.StatusPending:
			pending++
		case devices.StatusApproved:
			if !dev.LastSeenAt.IsZero() && now.Sub(dev.LastSeenAt) <= onlineWindow {
				online++
			}
		}
	}

	// Managed skills: distinct skill names in the registry.
	skills, err := d.Registry.ListSkills(r.Context())
	if err != nil {
		d.logErr("dashboard: list skills", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	// Update dimensions: reuse the exact §13.7 algorithm. local_and_upstream
	// items are the "conflict" contribution to high-risk.
	upd, err := d.aggregateUpdates(r)
	if err != nil {
		d.logErr("dashboard: aggregate updates", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	conflicts := dimensionCount(upd, dimLocalAndUpstream)

	// Untracked skills across all devices (drift the Updates page does not
	// surface). A skill the registry has no version for is unmanaged content
	// sitting on a device — a genuine risk to track.
	untracked, err := d.countUntracked(r, devs)
	if err != nil {
		d.logErr("dashboard: count untracked", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}

	// Failed deployments.
	var failed int
	if d.Deploy != nil {
		jobs, err := d.Deploy.List(r.Context(), deploy.ListFilter{Status: deploy.StatusFailed, Limit: failedJobScanLimit})
		if err != nil {
			d.logErr("dashboard: list failed deployments", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		failed = len(jobs)
	}

	metrics := dashboardMetrics{
		OnlineDevices:     online,
		ManagedSkills:     len(skills),
		LocalEdits:        upd.Summary.LocalEdits,
		UpstreamUpdates:   upd.Summary.UpstreamUpdates,
		FailedDeployments: failed,
		HighRiskItems:     untracked + conflicts + failed,
		PendingDevices:    pending,
		UntrackedSkills:   untracked,
	}

	// Action items: only surface non-zero ones, in the order an operator
	// should tackle them (approve gating devices first, then resolve
	// conflicts, then upstream updates, then failures).
	var actions []dashboardActionItem
	addAction := func(key, label string, count int) {
		if count > 0 {
			actions = append(actions, dashboardActionItem{Key: key, Count: count, Label: label})
		}
	}
	addAction("approve_devices", "审批待批准设备", pending)
	addAction("resolve_conflicts", "处理本地修改 + 上游更新冲突", conflicts)
	addAction("review_upstream", "查看上游更新", metrics.UpstreamUpdates)
	addAction("review_local_edits", "查看本地修改", metrics.LocalEdits)
	addAction("retry_failed", "重试失败的部署", failed)
	addAction("track_untracked", "纳管未跟踪的 Skills", untracked)
	if actions == nil {
		actions = []dashboardActionItem{}
	}

	writeJSON(w, http.StatusOK, dashboardResponse{Metrics: metrics, ActionItems: actions})
}

// dimensionCount returns the item count of one dimension in an updates payload.
func dimensionCount(resp updatesResponse, key string) int {
	for _, dim := range resp.Dimensions {
		if dim.Key == key {
			return len(dim.Items)
		}
	}
	return 0
}

// countUntracked walks every device's drift and counts (name, device, tool,
// scope) tuples the registry has no version for. Same content_sha256 guard as
// the Updates page rides through drift.ComputeDeviceDrift.
func (d Deps) countUntracked(r *http.Request, devs []devices.Device) (int, error) {
	lister := registryVersionLister{reg: d.Registry}
	var n int
	for _, dev := range devs {
		drifts, err := drift.ComputeDeviceDrift(r.Context(), d.DB, lister, dev.ID)
		if err != nil {
			return 0, err
		}
		for _, dr := range drifts {
			if dr.LocalState == drift.StateUntracked {
				n++
			}
		}
	}
	return n, nil
}
