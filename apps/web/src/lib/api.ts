// API client + helpers for the SkillFleet WebUI.
//
// All endpoints are same-origin (no CORS). For writes we forward the
// sf_csrf cookie value as the X-CSRF-Token header so the server's
// double-submit check passes (see internal/csrf).
//
// Errors thrown by the helpers are ApiError instances that preserve
// the server's `error` code and human-readable message; the UI uses
// the code for branching and the message for fallback display.

export type SetupStatus = { setup_required: boolean }

export type User = {
  user_id: string
  username: string
  expires_at: number
}

export type EnrollmentToken = {
  id: string
  status: "pending" | "used" | "revoked"
  created_at: number
  expires_at: number
  used_at?: number
}

// Device mirrors devices.Device on the server. last_seen_at is omitted
// when the agent has never reported; the UI surfaces this as "never".
export type Device = {
  id: string
  name: string
  hostname?: string
  os?: string
  arch?: string
  agent_version?: string
  status: "pending" | "approved" | "revoked"
  created_at: number
  last_seen_at?: number
}

// EffectiveState is the normalised enable/disable vocabulary shared by
// every adapter (mirrors internal/adapters.EffectiveState).
export type EffectiveState =
  | "on"
  | "off"
  | "name-only"
  | "user-invocable-only"
  | "ask"
  | "unknown"

export type InventoryWarning = {
  code: string
  message: string
}

// InventorySkill is one row of the device's tool x scope x skill matrix.
export type InventorySkill = {
  tool_key: string
  scope: "user" | "project" | "system"
  name: string
  skill_path: string
  has_skill_md: boolean
  description?: string
  effective_state: EffectiveState
  native_state?: string
  content_sha256?: string
  file_count: number
  total_bytes: number
  warnings?: InventoryWarning[]
}

// InventoryRun is the latest scan a device uploaded.
export type InventoryRun = {
  run_id: string
  started_at: number
  skill_count: number
  root_count: number
  agent_version?: string
  skills: InventorySkill[]
}

// LocalState classifies an installed skill against the registry (phase 7,
// v1.0 §8.2). Mirrors internal/drift.LocalState.
export type LocalState = "clean" | "local_modified" | "untracked"

// DriftSkill is one row of a device's drift classification: how the copy
// on disk compares to the registry, decided by content_sha256.
export type DriftSkill = {
  name: string
  tool_key: string
  scope: "user" | "project" | "system"
  local_sha?: string
  local_state: LocalState
  matched_version_id?: string
  registry_version_count: number
}

// DriftSummary counts each state across the device's skills.
export type DriftSummary = {
  clean: number
  local_modified: number
  untracked: number
}

// DeviceDrift is the GET /api/devices/{id}/drift payload.
export type DeviceDrift = {
  device_id: string
  skills: DriftSkill[]
  summary: DriftSummary
}

// --- Skills Registry (phase 4) ---

// SkillSummary is one row of the registry list (aggregated by name).
export type SkillSummary = {
  name: string
  version_count: number
  latest_version_id: string
  latest_label?: string
  latest_kind: string
  updated_at: number
  // bound|unbound (phase 6). Present whenever the server has a source store
  // wired; defaults to "unbound" for skills with no binding.
  source_state?: SourceState
}

// SkillVersion is one entry in a skill's version history.
export type SkillVersion = {
  id: string
  version_label?: string
  kind: string
  content_sha256: string
  base_version_id?: string
  file_count: number
  total_bytes: number
  created_at: number
}

export type SkillDetail = {
  name: string
  versions: SkillVersion[]
  // Source binding (phase 6). source_state is always present (bound|unbound);
  // source and last_checked_at appear only when a binding exists.
  source?: SourceView
  source_state: SourceState
  last_checked_at?: number
}

// --- Source binding (phase 6) ---

export type SourceState = "bound" | "unbound"

// upstream_state mirrors v1.0 §11.3, the outcome of an update check.
export type UpstreamState =
  | "up_to_date"
  | "update_available"
  | "remote_changed_no_skill_change"
  | "check_failed"

// SourceView mirrors the server's sourceView: the bound upstream's
// coordinates as the Source Tab renders them.
export type SourceView = {
  id: string
  name: string
  source_type: string
  url?: string
  provider?: string
  owner?: string
  repo?: string
  ref_type?: string
  ref_name?: string
  subdir?: string
  last_checked_at?: number
  last_remote_commit?: string
}

// BindSourceParams is the bind / preview request body. Only git/github
// types are fetchable in phase 6; ref_type defaults to "branch" server-side.
export type BindSourceParams = {
  source_type: string
  url: string
  provider?: string
  owner?: string
  repo?: string
  ref_type?: string
  ref_name?: string
  subdir?: string
}

// BindPreviewFile is one node of a preview's file tree.
export type BindPreviewFile = {
  path: string
  size: number
  binary: boolean
}

// BindPreview is the dry-run result of bind-source/preview: what WOULD be
// imported, fetched but not persisted.
export type BindPreview = {
  commit: string
  name?: string
  description?: string
  has_skill_md: boolean
  content_sha256: string
  file_count: number
  total_bytes: number
  files: BindPreviewFile[]
  warnings?: string[]
}

// BindSourceResult is the response to a successful bind: the new binding
// plus the baseline upstream version it captured.
export type BindSourceResult = {
  source: SourceView
  version: SkillVersion
}

// CheckUpdatesResult mirrors the server's checkUpdatesResponse. error is a
// short human string present only when upstream_state is check_failed.
export type CheckUpdatesResult = {
  upstream_state: UpstreamState
  remote_commit?: string
  remote_content_sha256?: string
  current_content_sha256?: string
  pending_version_id?: string
  last_checked_at: number
  error?: string
}

export type DetachResult = {
  detached: boolean
  source_state: SourceState
}

// --- Updates Page (phase 6 t9, §13.7) ---

// UpdateItem is one skill in a dimension. The upstream fields (baseline/
// pending*) carry data in the upstream dimensions; the device fields
// (device*/tool_key/scope/local_state) carry data in the local dimensions
// (phase 7). A field is omitted when empty, so each item only carries what
// its dimension populated.
export type UpdateItem = {
  name: string
  source_id?: string
  url?: string
  baseline_version_id?: string
  pending_version_id?: string
  pending_content_sha256?: string
  pending_created_at?: number
  // Local dimensions: where the locally-modified copy lives + its drift.
  device_id?: string
  device_name?: string
  tool_key?: string
  scope?: string
  local_state?: string
  local_sha?: string
}

// UpdateDimension is one §13.7 group. pending marks dimensions whose data
// lands in a later phase, so the UI shows a placeholder.
export type UpdateDimension = {
  key: string
  label: string
  pending: boolean
  items: UpdateItem[]
}

export type UpdatesSummary = {
  upstream_updates: number
  local_edits: number
  source_unknown: number
}

export type UpdatesResponse = {
  dimensions: UpdateDimension[]
  summary: UpdatesSummary
}

// --- Upstream diff (phase 6 t10, §17 task 6) ---

export type DiffStatus = "added" | "removed" | "modified" | "unchanged"

// DiffFile is one path's two-way comparison. base/target content is present
// only for editable text files; binary/oversized files set editable=false and
// omit content (the UI shows a "binary changed" marker).
export type DiffFile = {
  path: string
  status: DiffStatus
  editable: boolean
  binary: boolean
  base_present: boolean
  target_present: boolean
  base_content?: string
  target_content?: string
  base_size: number
  target_size: number
}

// UpstreamDiff is the two-way diff between a bound skill's baseline upstream
// and its pending upstream version. has_update is false when only the baseline
// exists (nothing pending to diff).
export type UpstreamDiff = {
  name: string
  has_update: boolean
  base_version_id?: string
  target_version_id?: string
  files: DiffFile[]
  unchanged: number
}

// SHACompare reports how the local fingerprint relates to a registry side.
// "unknown" when a sha is missing (no fingerprint), never silently "same".
export type SHACompare = "same" | "different" | "unknown"

// LocalSide is the device copy's standing in a three-way diff (phase 7).
// Content is withheld (sha-only) — the agent reports a fingerprint, not
// bytes, so per-file local diff is deferred to Phase 8.
export type LocalSide = {
  device_id: string
  tool_key: string
  scope: string
  sha?: string
  content_available: boolean
  vs_base: SHACompare
  vs_remote: SHACompare
}

// ThreeWayDiff is base | local | remote for a bound skill (§8.5). base vs
// remote (both registry versions) diff at file level via `files`; local is
// summarised by its sha relationship to base/remote. has_remote_update is
// false when there is no pending upstream version (files empty).
export type ThreeWayDiff = {
  name: string
  has_remote_update: boolean
  base_version_id?: string
  remote_version_id?: string
  local?: LocalSide
  files: DiffFile[]
  unchanged: number
}

// CaptureLocalParams is the body for capturing a local tree as a new
// local_edit version. files carry UTF-8 text; base_version_id records the
// version the copy was edited from (optional provenance).
export type CaptureLocalParams = {
  files: { path: string; content: string }[]
  base_version_id?: string
  version_label?: string
  device_id?: string
  tool_key?: string
  scope?: string
}

// --- Deployments (phase 8) ---

// DeployFileSpec is one file in a resolved plan (mirrors deploy.FileSpec).
export type DeployFileSpec = {
  path: string
  sha256: string
  size: number
  exec?: boolean
  binary?: boolean
}

// DeployPlan is the server-resolved install spec returned by the plan
// dry-run (mirrors deploy.Plan; only the fields the UI shows are typed).
export type DeployPlan = {
  version_id: string
  skill_name: string
  content_sha256: string
  archive_sha256: string
  archive_bytes: number
  download_path: string
  files: DeployFileSpec[]
}

// DeployRequestBody is the JSON for plan / execute.
export type DeployRequestBody = {
  skill_name: string
  version_id: string
  tool_key?: string
  scope?: string
  root_id?: string
  device_id?: string
}

// StateChangeBody is the JSON for POST /api/deployments/state-change: flip
// a skill's native enable/disable state on one device (phase 9). The
// server validates desired_state against the per-tool capability matrix
// and rejects (422) a state the tool can't represent.
export type StateChangeBody = {
  skill_name: string
  tool_key: string
  scope: string
  root_id?: string
  device_id: string
  desired_state: EffectiveState
}

// STATE_CHANGE_SUPPORT mirrors the server's statematrix (internal/deploy):
// which target states each tool can natively represent. A tool absent
// here supports no state changes (the matrix control is disabled). Keep in
// sync with deploy.SupportedStates — the server is authoritative and will
// 422 a mismatch, this just shapes the UI so the operator isn't offered an
// impossible option.
export const STATE_CHANGE_SUPPORT: Record<string, EffectiveState[]> = {
  "claude-code": ["on", "name-only", "user-invocable-only", "off"],
  codex: ["on", "off"],
  opencode: ["on", "ask", "off"],
}

// supportedStatesForTool returns the target states a tool can be set to,
// or [] when the tool supports no state changes.
export function supportedStatesForTool(toolKey: string): EffectiveState[] {
  return STATE_CHANGE_SUPPORT[toolKey] ?? []
}

export type DeploymentStatus =
  | "pending"
  | "claimed"
  | "running"
  | "succeeded"
  | "failed"
  | "expired"

// DeploymentJob is one deployment_jobs row, as the list/execute APIs
// project it (mirrors api.deploymentJobView).
export type DeploymentJob = {
  id: string
  device_id: string
  operation: "install" | "rollback"
  status: DeploymentStatus
  skill_name?: string
  version_id?: string
  created_at: number
  updated_at: number
  error_code?: string
  error_message?: string
  rolled_back?: boolean
  resolved_root?: string
}

// VersionFileEntry is one node of a version's file tree.
export type VersionFileEntry = {
  path: string
  sha256: string
  size: number
  exec: boolean
  binary: boolean
  editable: boolean
}

export type VersionFiles = {
  version_id: string
  name: string
  content_sha256: string
  files: VersionFileEntry[]
}

export type VersionFileContent = {
  path: string
  sha256: string
  size: number
  exec: boolean
  binary: boolean
  editable: boolean
  encoding: string
  content?: string
}

// DraftFile mirrors one skill_draft_files row as the API renders it.
export type DraftFile = {
  path: string
  is_binary: boolean
  size: number
  encoding?: string
  sha256?: string
  content?: string
}

export type Draft = {
  id: string
  name: string
  title?: string
  status: "open" | "published" | "discarded"
  base_version_id?: string
  created_at: number
  updated_at: number
  files: DraftFile[]
}

export type ValidationIssue = {
  severity: "error" | "warning"
  code: string
  path?: string
  message: string
  /** 1-based line within `path`; absent when the finding has no position. */
  line?: number
  /** 1-based column within `path`; absent when no column is known. */
  col?: number
}

export type ValidateResult = {
  ok: boolean
  issues: ValidationIssue[]
}

export type PublishResult = {
  version_id: string
  name: string
  warnings?: ValidationIssue[]
}


// CreateEnrollmentTokenResponse carries the plaintext token exactly
// once. The WebUI surfaces it in a copy-once banner; afterwards only
// the metadata (id + timestamps + status) is retrievable via /list.
export type CreateEnrollmentTokenResponse = EnrollmentToken & {
  token: string
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

// apiErrorMessage normalises a caught value into a user-facing string:
// an ApiError yields its server message; anything else yields the
// caller's fallback. This is the single source for the
// `err instanceof ApiError ? err.message : "…"` idiom that recurred
// across ~30 catch blocks.
export function apiErrorMessage(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.message : fallback
}

const JSON_CT = "application/json"

function readCookie(name: string): string | null {
  const prefix = name + "="
  for (const part of document.cookie.split(";")) {
    const trimmed = part.trim()
    if (trimmed.startsWith(prefix)) {
      return decodeURIComponent(trimmed.slice(prefix.length))
    }
  }
  return null
}

// addCsrfHeader echoes the sf_csrf double-submit cookie into a write
// request's header. Reads don't need it, but sending it when present is
// harmless. Mutates and returns the same headers object.
function addCsrfHeader(headers: Record<string, string>): Record<string, string> {
  const csrf = readCookie("sf_csrf")
  if (csrf) headers["X-CSRF-Token"] = csrf
  return headers
}

// parseResponse turns a fetch Response into T, mapping non-2xx (and
// unparseable) bodies to ApiError. 204 yields undefined. Shared by every
// caller so error shape stays uniform.
async function parseResponse<T>(res: Response): Promise<T> {
  if (res.status === 204) {
    return undefined as T
  }
  const text = await res.text()
  let parsed: unknown = null
  if (text.length > 0) {
    try {
      parsed = JSON.parse(text)
    } catch {
      // Treat unparseable bodies as opaque error messages.
      if (!res.ok) {
        throw new ApiError(res.status, "bad_response", text.slice(0, 200))
      }
    }
  }
  if (!res.ok) {
    const obj = parsed as { error?: string; message?: string } | null
    throw new ApiError(res.status, obj?.error ?? `http_${res.status}`, obj?.message ?? res.statusText)
  }
  return parsed as T
}

async function request<T>(
  method: "GET" | "POST" | "PUT" | "DELETE" | "PATCH",
  path: string,
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers["Content-Type"] = JSON_CT
  if (method !== "GET") addCsrfHeader(headers)

  const res = await fetch(path, {
    method,
    headers,
    credentials: "same-origin",
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  return parseResponse<T>(res)
}

// requestBlob POSTs a raw binary body (e.g. a zip upload) with an
// explicit Content-Type. It shares request()'s CSRF + error handling
// but sends bytes instead of JSON.
async function requestBlob<T>(path: string, contentType: string, body: Blob | ArrayBuffer): Promise<T> {
  const headers = addCsrfHeader({ "Content-Type": contentType })
  const res = await fetch(path, { method: "POST", headers, credentials: "same-origin", body })
  return parseResponse<T>(res)
}

export const api = {
  status: () => request<SetupStatus>("GET", "/api/status"),
  setup: (code: string, username: string, password: string) =>
    request<User>("POST", "/api/setup", { code, username, password }),
  login: (username: string, password: string) =>
    request<User>("POST", "/api/login", { username, password }),
  me: () => request<User>("GET", "/api/me"),
  logout: () => request<void>("POST", "/api/logout"),

  listEnrollmentTokens: () =>
    request<{ tokens: EnrollmentToken[] }>("GET", "/api/enrollment-tokens"),
  createEnrollmentToken: () =>
    request<CreateEnrollmentTokenResponse>("POST", "/api/enrollment-tokens"),
  revokeEnrollmentToken: (id: string) =>
    request<void>("POST", `/api/enrollment-tokens/${encodeURIComponent(id)}/revoke`),

  listDevices: () => request<{ devices: Device[] }>("GET", "/api/devices"),
  approveDevice: (id: string) =>
    request<Device>("POST", `/api/devices/${encodeURIComponent(id)}/approve`),
  revokeDevice: (id: string) =>
    request<Device>("POST", `/api/devices/${encodeURIComponent(id)}/revoke`),
  deviceInventory: (id: string) =>
    request<{ run: InventoryRun | null }>(
      "GET",
      `/api/devices/${encodeURIComponent(id)}/inventory`,
    ),

  deviceDrift: (id: string) =>
    request<DeviceDrift>("GET", `/api/devices/${encodeURIComponent(id)}/drift`),

  // --- Deployments (phase 8) ---
  planDeployment: (body: DeployRequestBody) =>
    request<{ plan: DeployPlan }>("POST", "/api/deployments/plan", body),
  executeDeployment: (body: DeployRequestBody) =>
    request<DeploymentJob>("POST", "/api/deployments/execute", body),
  rollbackDeployment: (jobId: string) =>
    request<DeploymentJob>(
      "POST",
      `/api/deployments/${encodeURIComponent(jobId)}/rollback`,
    ),
  listDeployments: (params?: { device?: string; skill?: string; status?: string }) => {
    const q = new URLSearchParams()
    if (params?.device) q.set("device", params.device)
    if (params?.skill) q.set("skill", params.skill)
    if (params?.status) q.set("status", params.status)
    const qs = q.toString()
    return request<{ jobs: DeploymentJob[] }>(
      "GET",
      qs ? `/api/deployments?${qs}` : "/api/deployments",
    )
  },
  // changeSkillState enqueues a state_change job (phase 9). The agent
  // edits the tool's out-of-band config (skillOverrides / config.toml /
  // permission.skill) on its next poll; the new state shows up after the
  // device's next inventory scan. request() attaches CSRF automatically.
  changeSkillState: (body: StateChangeBody) =>
    request<DeploymentJob>("POST", "/api/deployments/state-change", body),

  // --- Skills Registry (phase 4) ---
  listSkills: () => request<{ skills: SkillSummary[] }>("GET", "/api/skills"),
  createSkill: (name: string, description?: string) =>
    request<SkillDetail>("POST", "/api/skills", { name, description: description ?? "" }),
  importSkillZip: (zip: Blob, name?: string) =>
    requestBlob<SkillDetail>(
      name ? `/api/skills?name=${encodeURIComponent(name)}` : "/api/skills",
      "application/zip",
      zip,
    ),
  getSkill: (name: string) =>
    request<SkillDetail>("GET", `/api/skills/${encodeURIComponent(name)}`),

  versionFiles: (versionId: string) =>
    request<VersionFiles>("GET", `/api/skill-versions/${encodeURIComponent(versionId)}/files`),
  versionFile: (versionId: string, path: string) =>
    request<VersionFileContent>(
      "GET",
      `/api/skill-versions/${encodeURIComponent(versionId)}/files/${encodePackagePath(path)}`,
    ),

  // Drafts.
  createDraft: (params: { name?: string; title?: string; base_version_id?: string }) =>
    request<Draft>("POST", "/api/skill-drafts", params),
  getDraft: (id: string) => request<Draft>("GET", `/api/skill-drafts/${encodeURIComponent(id)}`),
  putDraftFile: (id: string, path: string, content: string, convertToUtf8 = false) =>
    request<DraftFile>(
      "PUT",
      `/api/skill-drafts/${encodeURIComponent(id)}/files/${encodePackagePath(path)}`,
      convertToUtf8 ? { content, convert_to_utf8: true } : { content },
    ),
  createDraftFile: (id: string, path: string, content: string, convertToUtf8 = false) =>
    request<DraftFile>(
      "POST",
      `/api/skill-drafts/${encodeURIComponent(id)}/files/${encodePackagePath(path)}`,
      convertToUtf8 ? { content, convert_to_utf8: true } : { content },
    ),
  deleteDraftFile: (id: string, path: string) =>
    request<void>(
      "DELETE",
      `/api/skill-drafts/${encodeURIComponent(id)}/files/${encodePackagePath(path)}`,
    ),
  deleteDraft: (id: string) =>
    request<void>("DELETE", `/api/skill-drafts/${encodeURIComponent(id)}`),
  validateDraft: (id: string) =>
    request<ValidateResult>("POST", `/api/skill-drafts/${encodeURIComponent(id)}/validate`),
  publishDraft: (id: string) =>
    request<PublishResult>("POST", `/api/skill-drafts/${encodeURIComponent(id)}/publish`),

  // --- Source binding (phase 6) ---
  // previewBindSource dry-runs a fetch and reports what would be bound,
  // persisting nothing. bindSource performs the real binding (fetch + record
  // + baseline upstream version). Both are POSTs (network side effects) so
  // request() attaches the CSRF header automatically.
  previewBindSource: (name: string, params: BindSourceParams) =>
    request<BindPreview>("POST", `/api/skills/${encodeURIComponent(name)}/bind-source/preview`, params),
  bindSource: (name: string, params: BindSourceParams) =>
    request<BindSourceResult>("POST", `/api/skills/${encodeURIComponent(name)}/bind-source`, params),
  checkUpdates: (name: string) =>
    request<CheckUpdatesResult>("POST", `/api/skills/${encodeURIComponent(name)}/check-updates`),
  detachSource: (name: string) =>
    request<DetachResult>("POST", `/api/skills/${encodeURIComponent(name)}/detach-source`),

  // Updates Page (§13.7): skills grouped by update dimension + summary.
  listUpdates: () => request<UpdatesResponse>("GET", "/api/updates"),

  // Upstream diff (§17 task 6): two-way diff between a bound skill's baseline
  // and its pending upstream version.
  upstreamDiff: (name: string) =>
    request<UpstreamDiff>("GET", `/api/skills/${encodeURIComponent(name)}/upstream-diff`),

  // Three-way diff (phase 7, §8.5): base | local | remote. device_id/tool_key/
  // scope locate the local side; omit them for base-vs-remote only.
  threeWayDiff: (name: string, local?: { deviceId: string; toolKey?: string; scope?: string }) => {
    const qs = new URLSearchParams()
    if (local) {
      qs.set("device_id", local.deviceId)
      if (local.toolKey) qs.set("tool_key", local.toolKey)
      if (local.scope) qs.set("scope", local.scope)
    }
    const suffix = qs.toString() ? `?${qs.toString()}` : ""
    return request<ThreeWayDiff>(
      "GET",
      `/api/skills/${encodeURIComponent(name)}/three-way-diff${suffix}`,
    )
  },

  // Capture local (phase 7, §8.3): publish a caller-supplied local tree as a
  // new local_edit version. POST → CSRF attached automatically.
  captureLocal: (name: string, body: CaptureLocalParams) =>
    request<{ version: SkillVersion }>(
      "POST",
      `/api/skills/${encodeURIComponent(name)}/capture-local`,
      body,
    ),
}

// encodePackagePath percent-encodes each segment of a package-relative
// path while preserving the "/" separators, so multi-segment paths map
// onto the {path...} wildcard route correctly.
function encodePackagePath(path: string): string {
  return path
    .split("/")
    .map((seg) => encodeURIComponent(seg))
    .join("/")
}
