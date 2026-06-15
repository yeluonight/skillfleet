import { CheckCircle2, FileMinus, FilePen, FilePlus, type LucideIcon } from "lucide-react"
import { useTranslation } from "react-i18next"
import type { ParseKeys } from "i18next"

import { cn } from "@/lib/utils"
import type { DiffStatus } from "@/lib/api"

// Per-status presentation for file diffs (added/removed/modified/unchanged),
// shared by the two-way (UpstreamDiffView) and three-way (ThreeWayMergeView)
// viewers. Triple-encoded (§13.8.7): a semantic state colour token, a Lucide
// icon, and a localised label; the English term rides along as the tooltip.

type StateName = "clean" | "danger" | "warn" | "muted"

const DIFF_STATUS_META: Record<
  DiffStatus,
  { state: StateName; sign: string; Icon: LucideIcon }
> = {
  added: { state: "clean", sign: "+", Icon: FilePlus },
  removed: { state: "danger", sign: "−", Icon: FileMinus },
  modified: { state: "warn", sign: "~", Icon: FilePen },
  unchanged: { state: "muted", sign: "", Icon: CheckCircle2 },
}

function textClass(state: StateName): string {
  switch (state) {
    case "clean":
      return "text-state-clean-600"
    case "danger":
      return "text-state-danger-600"
    case "warn":
      return "text-state-warn-600"
    case "muted":
      return "text-muted-foreground"
  }
}

function badgeClass(state: StateName): string {
  switch (state) {
    case "clean":
      return "bg-state-clean-50 text-state-clean-600 border-state-clean-500/30"
    case "danger":
      return "bg-state-danger-50 text-state-danger-600 border-state-danger-500/30"
    case "warn":
      return "bg-state-warn-50 text-state-warn-600 border-state-warn-500/30"
    case "muted":
      return "bg-state-muted-50 text-state-muted-600 border-state-muted-500/30"
  }
}

export function DiffStatusIcon({ status }: { status: DiffStatus }) {
  const { t } = useTranslation()
  const { Icon, state } = DIFF_STATUS_META[status]
  return (
    <Icon
      className={cn("size-3.5 shrink-0", textClass(state))}
      aria-label={t(`status.tip.${status}` as ParseKeys)}
    />
  )
}

export function DiffStatusBadge({ status }: { status: DiffStatus }) {
  const { t } = useTranslation()
  const { state, sign } = DIFF_STATUS_META[status]
  return (
    <span
      title={t(`status.tip.${status}` as ParseKeys)}
      className={cn(
        "shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-medium",
        badgeClass(state)
      )}
    >
      {sign}
      {t(`status.diff.${status}` as ParseKeys)}
    </span>
  )
}
