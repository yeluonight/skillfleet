package api

import "net/http"

// Capability guards centralise the "dependency not wired → 503" checks
// that every handler performs before touching an optional Deps field.
// Each returns true when the capability is present; on absence it has
// already written the canonical 503 (code "<cap>_unavailable", message
// "<cap> not configured") and the caller must return immediately.
//
// The code/message pairs are byte-for-byte identical to the inline checks
// they replace — see capability_test.go, which locks them down (the
// pre-refactor handlers had no 503-path test coverage).

// requireRegistry guards d.Registry.
func (d Deps) requireRegistry(w http.ResponseWriter) bool {
	if d.Registry == nil {
		writeError(w, http.StatusServiceUnavailable, "registry_unavailable", "registry not configured")
		return false
	}
	return true
}

// requireDrafts guards d.Drafts.
func (d Deps) requireDrafts(w http.ResponseWriter) bool {
	if d.Drafts == nil {
		writeError(w, http.StatusServiceUnavailable, "drafts_unavailable", "drafts not configured")
		return false
	}
	return true
}

// requireDeploy guards d.Deploy.
func (d Deps) requireDeploy(w http.ResponseWriter) bool {
	if d.Deploy == nil {
		writeError(w, http.StatusServiceUnavailable, "deploy_unavailable", "deployment not configured")
		return false
	}
	return true
}

// requireSources guards d.Sources, and d.Fetcher too when needFetcher is
// set. Bind/preview/check need the fetcher (they pull remote content);
// detach only needs the store. Both absences map to the same
// "sources_unavailable" code, matching the pre-refactor behaviour.
func (d Deps) requireSources(w http.ResponseWriter, needFetcher bool) bool {
	if d.Sources == nil || (needFetcher && d.Fetcher == nil) {
		writeError(w, http.StatusServiceUnavailable, "sources_unavailable", "source binding not configured")
		return false
	}
	return true
}

// requireRegistryAndSources guards the read-only diff/updates handlers
// that need both the registry and a source binding. Both absences map to
// "sources_unavailable" — preserving the original compound check's single
// error code (Registry being nil here still reports sources_unavailable,
// not registry_unavailable).
func (d Deps) requireRegistryAndSources(w http.ResponseWriter) bool {
	if d.Registry == nil || d.Sources == nil {
		writeError(w, http.StatusServiceUnavailable, "sources_unavailable", "source binding not configured")
		return false
	}
	return true
}

// requireDeployStack guards deploy-execute, which needs both the registry
// (to resolve a version into a plan) and the deploy store (to enqueue the
// job). Both absences map to "deploy_unavailable" — preserving the
// original compound check's single error code.
func (d Deps) requireDeployStack(w http.ResponseWriter) bool {
	if d.Registry == nil || d.Deploy == nil {
		writeError(w, http.StatusServiceUnavailable, "deploy_unavailable", "deployment not configured")
		return false
	}
	return true
}
