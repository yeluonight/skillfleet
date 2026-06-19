package api

import (
	"cmp"
	"net/http"
	"slices"

	"github.com/yeluonight/skillfleet/internal/devices"
	"github.com/yeluonight/skillfleet/internal/drift"
	"github.com/yeluonight/skillfleet/internal/registry"
)

// updates_handlers.go serves the Updates Page (v1.0 §13.7): skills grouped
// by the six update dimensions. Phase 6 filled exactly ONE — "upstream has
// an update". Phase 7 t8 adds two more from device drift: "local edits" and
// "local edits + upstream update". The remaining three (source unknown,
// source gone, auth required) still need Phase 8 machinery (device-inferred
// sources, stored credentials), so they stay empty/pending placeholders the
// UI renders rather than silently omitting.
//
// How "upstream has an update" is derived (no upstream_state column exists —
// see the t6 decision): a bound skill whose registry holds MORE THAN ONE
// upstream-kind version has had at least one update check publish a pending
// version on top of the bind baseline. The newest upstream version is that
// pending update; the oldest is the baseline. We never infer an update from a
// moved commit — the pending version only exists because the §8.4 engine's
// content_sha256 comparison already decided the subtree genuinely changed.
// That is the same core guard, surfaced here as "if a pending version exists,
// the content really differs."
//
// How "local edits" is derived (phase 7): walk every device's latest
// inventory run through the drift engine (internal/drift) and collect the
// skills classified local_modified — the device's content_sha256 matches no
// registry version of that name. Same content_sha256 guard: a device whose
// bytes match a known version is clean and never appears here. "local +
// upstream" is the intersection — a locally-modified skill whose name also
// has a pending upstream update.

// updateDimension is one §13.7 group. Pending marks dimensions whose data
// lands in a later phase, so the UI can show a placeholder instead of an
// empty-but-final list.
type updateDimension struct {
	Key     string       `json:"key"`
	Label   string       `json:"label"`
	Pending bool         `json:"pending"`
	Items   []updateItem `json:"items"`
}

// updateItem is one skill in a dimension. The upstream fields
// (Baseline/Pending*) carry data in the upstream dimensions; the device
// fields (Device*/ToolKey/Scope/LocalState) carry data in the local
// dimensions. A field is omitted when empty so each item only shows what its
// dimension populated.
type updateItem struct {
	Name string `json:"name"`
	// SourceID/URL identify the binding the update came from (upstream dims).
	SourceID string `json:"source_id,omitempty"`
	URL      string `json:"url,omitempty"`
	// BaselineVersionID is the bind-time upstream snapshot; PendingVersionID
	// is the newest upstream version (the update awaiting Phase 7 adoption).
	BaselineVersionID string `json:"baseline_version_id,omitempty"`
	PendingVersionID  string `json:"pending_version_id,omitempty"`
	// PendingContentSHA256 lets the UI show the changed content identity;
	// PendingCreatedAt is when the update check captured it (ms epoch).
	PendingContentSHA256 string `json:"pending_content_sha256,omitempty"`
	PendingCreatedAt     int64  `json:"pending_created_at,omitempty"`

	// Device fields (local dimensions): which device/tool/scope holds the
	// locally-modified copy, and its drift state.
	DeviceID   string `json:"device_id,omitempty"`
	DeviceName string `json:"device_name,omitempty"`
	ToolKey    string `json:"tool_key,omitempty"`
	Scope      string `json:"scope,omitempty"`
	LocalState string `json:"local_state,omitempty"`
	LocalSHA   string `json:"local_sha,omitempty"`
}

// updatesResponse is the GET /api/updates payload: the dimension groups plus
// a small summary the Dashboard cards read (§13.1).
type updatesResponse struct {
	Dimensions []updateDimension `json:"dimensions"`
	Summary    updatesSummary    `json:"summary"`
}

// updatesSummary feeds the Dashboard stat cards. UpstreamUpdates and
// LocalEdits are real (phase 6 / phase 7); SourceUnknown is a Phase 8
// placeholder (always 0 here) kept in the shape so the card can render
// without a follow-up API change.
type updatesSummary struct {
	UpstreamUpdates int `json:"upstream_updates"`
	LocalEdits      int `json:"local_edits"`
	SourceUnknown   int `json:"source_unknown"`
}

// Dimension keys/labels mirror v1.0 §13.7. upstream_update (phase 6) and
// local_edit / local_and_upstream (phase 7 t8) carry data; the rest are
// placeholders.
const (
	dimUpstreamUpdate   = "upstream_update"
	dimLocalEdit        = "local_edit"
	dimLocalAndUpstream = "local_and_upstream"
	dimSourceUnknown    = "source_unknown"
	dimSourceGone       = "source_gone"
	dimAuthRequired     = "auth_required"
)

// handleListUpdates aggregates the Updates Page (§13.7). It walks every bound
// source for upstream updates, and every device for local modifications, then
// groups by dimension. Phase 6 filled the upstream dimension; phase 7 t8 adds
// the two local dimensions; the rest stay pending.
//
// Read-only (auth, no CSRF). A missing registry/source store is a 503 — the
// page can't be built without them.
func (d Deps) handleListUpdates(w http.ResponseWriter, r *http.Request) {
	if !d.requireRegistryAndSources(w) {
		return
	}
	resp, err := d.aggregateUpdates(r)
	if err != nil {
		d.logErr("updates: aggregate", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// aggregateUpdates builds the full §13.7 Updates payload (dimensions +
// summary). Split out from handleListUpdates so the Dashboard endpoint can
// reuse the exact same upstream/local algorithm without a second HTTP round
// trip. The caller is responsible for the requireRegistryAndSources guard.
func (d Deps) aggregateUpdates(r *http.Request) (updatesResponse, error) {
	srcs, err := d.Sources.ListAll(r.Context())
	if err != nil {
		return updatesResponse{}, err
	}

	items := make([]updateItem, 0)
	// upstreamNames lets the local pass classify a local edit as
	// "local + upstream" when the same skill name also has a pending update.
	upstreamNames := make(map[string]struct{})
	for _, src := range srcs {
		versions, err := d.Registry.ListByName(r.Context(), src.Name)
		if err != nil {
			return updatesResponse{}, err
		}
		// Collect upstream-kind versions. ListByName is created_at DESC, so
		// upstreams[0] is newest (the pending update) and the last is the
		// bind baseline.
		var upstreams []registry.Version
		for _, v := range versions {
			if v.Kind == registry.KindUpstream {
				upstreams = append(upstreams, v)
			}
		}
		if len(upstreams) < 2 {
			// 0 = not yet baselined (shouldn't happen for a bound skill); 1 =
			// baseline only, no update detected. Neither is an update.
			continue
		}
		newest := upstreams[0]
		baseline := upstreams[len(upstreams)-1]
		upstreamNames[src.Name] = struct{}{}
		items = append(items, updateItem{
			Name:                 src.Name,
			SourceID:             src.ID,
			URL:                  src.URL,
			BaselineVersionID:    baseline.ID,
			PendingVersionID:     newest.ID,
			PendingContentSHA256: newest.ContentSHA256,
			PendingCreatedAt:     newest.CreatedAt.UnixMilli(),
		})
	}
	// Stable, name-sorted output so the page doesn't reshuffle between loads.
	slices.SortFunc(items, func(a, b updateItem) int { return cmp.Compare(a.Name, b.Name) })

	// Index the upstream items by name so combined (local + upstream) items can
	// carry the upstream-side fields (source_id, baseline, pending) the local
	// pass doesn't know — without them the UI couldn't offer "view diff" or
	// "deploy update" on a combined row.
	upstreamByName := make(map[string]updateItem, len(items))
	for _, it := range items {
		upstreamByName[it.Name] = it
	}

	// Local pass: collect local_modified skills across all devices, splitting
	// those whose name also has a pending upstream update into the combined
	// dimension.
	localOnly, localAndUpstream, err := d.collectLocalEdits(r, upstreamNames)
	if err != nil {
		return updatesResponse{}, err
	}
	// Enrich combined items with their upstream-side fields.
	for i := range localAndUpstream {
		if up, ok := upstreamByName[localAndUpstream[i].Name]; ok {
			localAndUpstream[i].SourceID = up.SourceID
			localAndUpstream[i].URL = up.URL
			localAndUpstream[i].BaselineVersionID = up.BaselineVersionID
			localAndUpstream[i].PendingVersionID = up.PendingVersionID
			localAndUpstream[i].PendingContentSHA256 = up.PendingContentSHA256
			localAndUpstream[i].PendingCreatedAt = up.PendingCreatedAt
		}
	}

	return updatesResponse{
		Dimensions: []updateDimension{
			{Key: dimUpstreamUpdate, Label: "上游有更新", Pending: false, Items: items},
			{Key: dimLocalEdit, Label: "本地有修改", Pending: false, Items: localOnly},
			{Key: dimLocalAndUpstream, Label: "本地修改 + 上游更新", Pending: false, Items: localAndUpstream},
			{Key: dimSourceUnknown, Label: "来源未知", Pending: true, Items: []updateItem{}},
			{Key: dimSourceGone, Label: "来源失效", Pending: true, Items: []updateItem{}},
			{Key: dimAuthRequired, Label: "需要认证", Pending: true, Items: []updateItem{}},
		},
		Summary: updatesSummary{
			UpstreamUpdates: len(items),
			LocalEdits:      len(localOnly) + len(localAndUpstream),
			SourceUnknown:   0, // Phase 8 (device-inferred sources).
		},
	}, nil
}

// collectLocalEdits walks every device's drift and partitions the
// local_modified skills into "local only" and "local + upstream" (the latter
// when the skill name also has a pending upstream update). Both lists are
// sorted by (name, device, tool, scope) for stable output.
//
// The content_sha256 guard rides through drift.ComputeDeviceDrift: a device
// whose bytes match a registry version is clean and never lands here, so a
// moved commit / re-fingerprint that didn't change content is not a false
// local edit.
func (d Deps) collectLocalEdits(r *http.Request, upstreamNames map[string]struct{}) (localOnly, localAndUpstream []updateItem, err error) {
	localOnly = make([]updateItem, 0)
	localAndUpstream = make([]updateItem, 0)

	devs, err := devices.List(r.Context(), d.DB, 0)
	if err != nil {
		return nil, nil, err
	}

	lister := registryVersionLister{reg: d.Registry}
	for _, dev := range devs {
		drifts, err := drift.ComputeDeviceDrift(r.Context(), d.DB, lister, dev.ID)
		if err != nil {
			return nil, nil, err
		}
		for _, dr := range drifts {
			if dr.LocalState != drift.StateLocalModified {
				continue
			}
			item := updateItem{
				Name:       dr.Name,
				DeviceID:   dev.ID,
				DeviceName: dev.Name,
				ToolKey:    dr.ToolKey,
				Scope:      dr.Scope,
				LocalState: string(dr.LocalState),
				LocalSHA:   dr.LocalSHA,
			}
			if _, ok := upstreamNames[dr.Name]; ok {
				localAndUpstream = append(localAndUpstream, item)
			} else {
				localOnly = append(localOnly, item)
			}
		}
	}

	sortLocalItems(localOnly)
	sortLocalItems(localAndUpstream)
	return localOnly, localAndUpstream, nil
}

// sortLocalItems orders local-edit items by (name, device, tool, scope) so
// the page is stable across loads.
func sortLocalItems(items []updateItem) {
	slices.SortFunc(items, func(a, b updateItem) int {
		return cmp.Or(
			cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.DeviceID, b.DeviceID),
			cmp.Compare(a.ToolKey, b.ToolKey),
			cmp.Compare(a.Scope, b.Scope),
		)
	})
}
