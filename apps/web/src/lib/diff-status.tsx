import { CheckCircle2, FileMinus, FilePen, FilePlus } from "lucide-react"

import type { DiffStatus } from "@/lib/api"

// Per-status presentation for file diffs (added/removed/modified/unchanged),
// shared by the two-way (UpstreamDiffView) and three-way (ThreeWayMergeView)
// viewers. Both grew identical Icon/label/colour switches; this is the single
// source. `text` is the icon + badge-text colour; `bg` is the badge fill.
const DIFF_STATUS_META: Record<
  DiffStatus,
  { label: string; text: string; bg: string; Icon: typeof FilePlus }
> = {
  added: { label: "+新增", text: "text-emerald-600", bg: "bg-emerald-500/15", Icon: FilePlus },
  removed: { label: "−删除", text: "text-red-600", bg: "bg-red-500/15", Icon: FileMinus },
  modified: { label: "~修改", text: "text-amber-600", bg: "bg-amber-500/15", Icon: FilePen },
  unchanged: {
    label: "未变更",
    text: "text-muted-foreground",
    bg: "bg-muted",
    Icon: CheckCircle2,
  },
}

export function DiffStatusIcon({ status }: { status: DiffStatus }) {
  const { Icon, text } = DIFF_STATUS_META[status]
  return <Icon className={`size-3.5 shrink-0 ${text}`} aria-hidden />
}

export function DiffStatusBadge({ status }: { status: DiffStatus }) {
  const { label, text, bg } = DIFF_STATUS_META[status]
  return (
    <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${bg} ${text}`}>
      {label}
    </span>
  )
}
