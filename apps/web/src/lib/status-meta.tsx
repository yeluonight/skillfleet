import {
  CheckCircle2,
  XCircle,
  Loader2,
  CircleDot,
  Clock,
  Circle,
  Download,
  Undo2,
  ToggleLeft,
  FolderPlus,
  FolderMinus,
  Upload,
  FilePen,
  PackagePlus,
  HelpCircle,
  type LucideIcon,
} from "lucide-react"
import { useTranslation } from "react-i18next"
import type { ParseKeys } from "i18next"

import { cn } from "@/lib/utils"
import type { DeploymentJob, DeploymentStatus, EffectiveState, LocalState } from "@/lib/api"

// Centralised enum → presentation, mirroring lib/diff-status.tsx. Each status
// is triple-encoded (§13.8.7): a semantic state colour, a Lucide icon, and a
// localised label, with the canonical English term kept as the title tooltip.
//
// `state` selects the semantic colour family (t3 tokens); the badge renders
// the three-part token set bg-{state}-50 / text-{state}-600 /
// border-{state}-500/30 so no Tailwind palette colour is hardcoded.

type StateName = "clean" | "info" | "warn" | "danger" | "muted" | "fork"

// stateClasses returns the token triple for a semantic state. Kept as a
// function (not a lookup of full strings) so Tailwind's scanner sees each
// literal class name.
function stateClasses(state: StateName): string {
  switch (state) {
    case "clean":
      return "bg-state-clean-50 text-state-clean-600 border-state-clean-500/30"
    case "info":
      return "bg-state-info-50 text-state-info-600 border-state-info-500/30"
    case "warn":
      return "bg-state-warn-50 text-state-warn-600 border-state-warn-500/30"
    case "danger":
      return "bg-state-danger-50 text-state-danger-600 border-state-danger-500/30"
    case "fork":
      return "bg-state-fork-50 text-state-fork-600 border-state-fork-500/30"
    case "muted":
      return "bg-state-muted-50 text-state-muted-600 border-state-muted-500/30"
  }
}

// StatusBadge is the shared filled-pill renderer: token colours + icon +
// localised label + English tooltip. `spin` animates the icon (running state).
function StatusBadge({
  state,
  Icon,
  label,
  tooltip,
  spin = false,
}: {
  state: StateName
  Icon: LucideIcon
  label: string
  tooltip: string
  spin?: boolean
}) {
  return (
    <span
      title={tooltip}
      className={cn(
        "inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium",
        stateClasses(state)
      )}
    >
      <Icon className={cn("size-3 shrink-0", spin && "animate-spin")} aria-hidden />
      {label}
    </span>
  )
}

// --- Deployment job status (JobsList) ---

const DEPLOYMENT_STATUS_META: Record<
  DeploymentStatus,
  { state: StateName; Icon: LucideIcon; spin?: boolean }
> = {
  succeeded: { state: "clean", Icon: CheckCircle2 },
  failed: { state: "danger", Icon: XCircle },
  running: { state: "info", Icon: Loader2, spin: true },
  claimed: { state: "info", Icon: CircleDot },
  expired: { state: "muted", Icon: Clock },
  pending: { state: "muted", Icon: Circle },
}

export function DeploymentStatusBadge({ status }: { status: DeploymentStatus }) {
  const { t } = useTranslation()
  // Fall back to "pending" presentation for any unknown value.
  const meta = DEPLOYMENT_STATUS_META[status] ?? DEPLOYMENT_STATUS_META.pending
  const known = DEPLOYMENT_STATUS_META[status] ? status : "pending"
  return (
    <StatusBadge
      state={meta.state}
      Icon={meta.Icon}
      spin={meta.spin}
      label={t(`status.deploy.${known}` as ParseKeys)}
      tooltip={t(`status.tip.${known}` as ParseKeys)}
    />
  )
}

// --- Deployment operation (JobsList) ---

type DeploymentOperation = DeploymentJob["operation"]

const OPERATION_META: Record<DeploymentOperation, { state: StateName; Icon: LucideIcon }> = {
  install: { state: "info", Icon: Download },
  rollback: { state: "warn", Icon: Undo2 },
  state_change: { state: "muted", Icon: ToggleLeft },
  register_root: { state: "muted", Icon: FolderPlus },
  remove_root: { state: "muted", Icon: FolderMinus },
  capture_skill: { state: "info", Icon: Upload },
}

export function OperationBadge({ operation }: { operation: DeploymentOperation }) {
  const { t } = useTranslation()
  // Fall back to install presentation + the raw operation as its own label
  // for any value missing from the map, so an unmapped operation degrades to
  // a plain badge instead of throwing (which, with no ErrorBoundary, would
  // blank the page). Mirrors DeploymentStatusBadge's fallback.
  const meta = OPERATION_META[operation] ?? OPERATION_META.install
  const known = OPERATION_META[operation] ? operation : null
  return (
    <StatusBadge
      state={meta.state}
      Icon={meta.Icon}
      label={known ? t(`status.op.${known}` as ParseKeys) : operation}
      tooltip={known ? t(`status.tip.${known}` as ParseKeys) : operation}
    />
  )
}

// --- Local drift state (InventoryMatrix) ---

const LOCAL_STATE_META: Record<LocalState, { state: StateName; Icon: LucideIcon }> = {
  clean: { state: "muted", Icon: CheckCircle2 },
  local_modified: { state: "warn", Icon: FilePen },
  untracked: { state: "info", Icon: HelpCircle },
  not_deployed: { state: "muted", Icon: PackagePlus },
}

// LocalStateBadge renders a skill's drift against the registry (phase 7).
// undefined ⇒ drift not loaded / no row for this skill: show a quiet dash
// rather than implying a state we don't know.
export function LocalStateBadge({ state }: { state?: LocalState }) {
  const { t } = useTranslation()
  if (!state) {
    return <span className="text-muted-foreground text-xs">—</span>
  }
  const meta = LOCAL_STATE_META[state]
  return (
    <StatusBadge
      state={meta.state}
      Icon={meta.Icon}
      label={t(`status.local.${state}` as ParseKeys)}
      tooltip={t(`status.tip.${state}` as ParseKeys)}
    />
  )
}

// --- Effective enablement state (InventoryMatrix) ---

// Bare coloured text (no fill). off/unknown are muted; on is clean; ask is
// warn; the name-only / user-invocable-only partials are info.
const EFFECTIVE_STATE_META: Record<EffectiveState, StateName> = {
  on: "clean",
  off: "muted",
  ask: "warn",
  unknown: "muted",
  "name-only": "info",
  "user-invocable-only": "info",
}

const EFFECTIVE_STATE_TEXT: Record<StateName, string> = {
  clean: "text-state-clean-600",
  info: "text-state-info-600",
  warn: "text-state-warn-600",
  danger: "text-state-danger-600",
  fork: "text-state-fork-600",
  muted: "text-muted-foreground",
}

export function StateBadge({ state }: { state: EffectiveState }) {
  const { t } = useTranslation()
  const colour = EFFECTIVE_STATE_TEXT[EFFECTIVE_STATE_META[state] ?? "muted"]
  return (
    <span className={cn("font-medium", colour)} title={state}>
      {t(`status.effective.${state}` as ParseKeys)}
    </span>
  )
}
