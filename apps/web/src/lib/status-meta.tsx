import type { DeploymentStatus, EffectiveState, LocalState } from "@/lib/api"

// Centralised enum → {label, colour} presentation, mirroring the meta-table
// pattern of lib/diff-status.tsx. Each badge below grew an inline
// switch/ternary in its component; this is the single source so adding a
// status means editing one table, not hunting per-component renderers.
//
// `cls` bundles the Tailwind background + text colour for a filled badge.
// EffectiveState is the exception: it renders as bare coloured text (no
// fill) with the raw enum value as its label, so its table carries only a
// `text` colour.

// --- Deployment job status (JobsList) ---

const DEPLOYMENT_STATUS_META: Record<DeploymentStatus, { label: string; cls: string }> = {
  succeeded: { label: "成功", cls: "bg-emerald-500/15 text-emerald-600" },
  failed: { label: "失败", cls: "bg-red-500/15 text-red-600" },
  running: { label: "执行中", cls: "bg-sky-500/15 text-sky-600" },
  claimed: { label: "已领取", cls: "bg-sky-500/15 text-sky-600" },
  expired: { label: "已过期", cls: "bg-muted text-muted-foreground" },
  pending: { label: "待处理", cls: "bg-muted text-muted-foreground" },
}

export function DeploymentStatusBadge({ status }: { status: DeploymentStatus }) {
  // Fall back to "pending" presentation for any unknown value, matching
  // the original switch's default arm.
  const { label, cls } = DEPLOYMENT_STATUS_META[status] ?? DEPLOYMENT_STATUS_META.pending
  return <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${cls}`}>{label}</span>
}

// --- Deployment operation (JobsList) ---

const OPERATION_META: Record<"install" | "rollback", { label: string; cls: string }> = {
  install: { label: "安装", cls: "bg-sky-500/15 text-sky-600" },
  rollback: { label: "回滚", cls: "bg-amber-500/15 text-amber-600" },
}

export function OperationBadge({ operation }: { operation: "install" | "rollback" }) {
  const { label, cls } = OPERATION_META[operation]
  return <span className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${cls}`}>{label}</span>
}

// --- Local drift state (InventoryMatrix) ---

const LOCAL_STATE_META: Record<LocalState, { label: string; cls: string }> = {
  clean: { label: "clean", cls: "bg-muted text-muted-foreground" },
  local_modified: { label: "local edit", cls: "bg-amber-500/15 text-amber-600" },
  untracked: { label: "untracked", cls: "bg-sky-500/15 text-sky-600" },
}

// LocalStateBadge renders a skill's drift against the registry (phase 7).
// undefined ⇒ drift not loaded / no row for this skill: show a quiet dash
// rather than implying a state we don't know.
export function LocalStateBadge({ state }: { state?: LocalState }) {
  if (!state) {
    return <span className="text-muted-foreground text-xs">—</span>
  }
  const { label, cls } = LOCAL_STATE_META[state]
  return (
    <span className={`inline-block rounded px-1.5 py-0.5 text-[10px] font-medium ${cls}`}>
      {label}
    </span>
  )
}

// --- Effective enablement state (InventoryMatrix) ---

// Bare coloured text, label = the raw enum value. off and unknown share the
// muted colour; name-only / user-invocable-only fall through to sky.
const EFFECTIVE_STATE_TEXT: Record<EffectiveState, string> = {
  on: "text-emerald-600",
  off: "text-muted-foreground",
  ask: "text-amber-600",
  unknown: "text-muted-foreground",
  "name-only": "text-sky-600",
  "user-invocable-only": "text-sky-600",
}

export function StateBadge({ state }: { state: EffectiveState }) {
  const colour = EFFECTIVE_STATE_TEXT[state] ?? "text-muted-foreground"
  return <span className={`font-medium ${colour}`}>{state}</span>
}
