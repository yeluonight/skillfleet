import { useState, type ReactNode } from "react"
import {
  AlertCircle,
  ArrowUpCircle,
  CheckCircle2,
  Clock,
  ExternalLink,
  Laptop,
  Loader2,
  Package,
  RefreshCw,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { formatRelativeTime } from "@/lib/time"
import type { UpdateDimension, UpdateItem, UpdatesResponse, UpdatesSummary } from "@/lib/api"

export type UpdatesCardProps = {
  data: UpdatesResponse | null
  error: string | null
  busy: boolean
  onRefresh: () => void
  onSelectSkill?: (name: string) => void
}

// UpdatesCard is a pure controlled surface for the Updates Page. Parents own
// API calls, CSRF, refresh state, errors, and any navigation on skill select.
export function UpdatesCard({ data, error, busy, onRefresh, onSelectSkill }: UpdatesCardProps) {
  const [renderedAt] = useState(() => Date.now())

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-3">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2 text-lg">
              <Package className="text-primary size-5" aria-hidden />
              更新
            </CardTitle>
            <CardDescription>按维度查看需要关注的 Skill 更新状态。</CardDescription>
          </div>
          <Button type="button" size="sm" variant="ghost" onClick={onRefresh} disabled={busy}>
            {busy ? (
              <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
              <RefreshCw className="size-4" aria-hidden />
            )}
            刷新
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {error ? (
          <Alert variant="destructive">
            <AlertCircle className="size-4" aria-hidden />
            <AlertTitle>加载失败</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        {data ? (
          <>
            <SummaryCards summary={data.summary} />
            <DimensionsList
              dimensions={data.dimensions}
              renderedAt={renderedAt}
              onSelectSkill={onSelectSkill}
            />
          </>
        ) : !error ? (
          <p className="text-muted-foreground text-sm">加载中…</p>
        ) : null}
      </CardContent>
    </Card>
  )
}

function SummaryCards({ summary }: { summary: UpdatesSummary }) {
  return (
    <div className="grid gap-3 sm:grid-cols-3">
      <SummaryCard
        label="上游可更新"
        value={summary.upstream_updates}
        icon={<ArrowUpCircle className="size-5" aria-hidden />}
        emphasis={summary.upstream_updates > 0 ? "amber" : "muted"}
      />
      <SummaryCard
        label="本地有修改"
        value={summary.local_edits}
        icon={<Laptop className="size-5" aria-hidden />}
        emphasis={summary.local_edits > 0 ? "sky" : "muted"}
      />
      <SummaryCard
        label="来源未知"
        value={summary.source_unknown}
        icon={<AlertCircle className="size-5" aria-hidden />}
        emphasis={summary.source_unknown > 0 ? "red" : "muted"}
      />
    </div>
  )
}

function SummaryCard({
  label,
  value,
  icon,
  emphasis,
}: {
  label: string
  value: number
  icon: ReactNode
  emphasis: "amber" | "muted" | "red" | "sky"
}) {
  const colour =
    emphasis === "amber"
      ? "text-amber-600"
      : emphasis === "red"
        ? "text-red-600"
        : emphasis === "sky"
          ? "text-sky-600"
          : "text-muted-foreground"

  return (
    <div className="rounded-md border bg-card px-3 py-3">
      <div className="flex items-center justify-between gap-3">
        <span className="text-muted-foreground text-sm">{label}</span>
        <span className={colour}>{icon}</span>
      </div>
      <div className={`mt-2 text-3xl font-semibold tabular-nums ${colour}`}>{value}</div>
    </div>
  )
}

function DimensionsList({
  dimensions,
  renderedAt,
  onSelectSkill,
}: {
  dimensions: UpdateDimension[]
  renderedAt: number
  onSelectSkill?: (name: string) => void
}) {
  return (
    <div className="space-y-3">
      {dimensions.map((dimension) => (
        <DimensionSection
          key={dimension.key}
          dimension={dimension}
          renderedAt={renderedAt}
          onSelectSkill={onSelectSkill}
        />
      ))}
    </div>
  )
}

function DimensionSection({
  dimension,
  renderedAt,
  onSelectSkill,
}: {
  dimension: UpdateDimension
  renderedAt: number
  onSelectSkill?: (name: string) => void
}) {
  if (dimension.pending) {
    return (
      <section className="rounded-md border border-dashed bg-muted/30 px-3 py-3">
        <DimensionHeader dimension={dimension} muted />
        <div className="text-muted-foreground mt-2 flex items-center gap-2 text-sm">
          <Clock className="size-4 shrink-0" aria-hidden />
          <span>此维度将在后续阶段提供数据。</span>
        </div>
      </section>
    )
  }

  return (
    <section className="rounded-md border px-3 py-3">
      <DimensionHeader dimension={dimension} />
      {dimension.items.length === 0 ? (
        <div className="text-muted-foreground mt-3 flex items-center gap-2 text-sm">
          <CheckCircle2 className="size-4 shrink-0 text-emerald-600" aria-hidden />
          <span>没有需要关注的项目</span>
        </div>
      ) : (
        <ul className="divide-border mt-3 divide-y rounded-md border">
          {dimension.items.map((item) => (
            <UpdateItemRow
              key={itemKey(item)}
              item={item}
              renderedAt={renderedAt}
              onSelectSkill={onSelectSkill}
            />
          ))}
        </ul>
      )}
    </section>
  )
}

// itemKey builds a stable React key for either item shape. Upstream items
// are unique by (name, pending version); local-edit items by (name, device,
// tool, scope) — the same skill name can be modified on several devices.
function itemKey(item: UpdateItem): string {
  if (item.device_id) {
    return `${item.name}:${item.device_id}:${item.tool_key ?? ""}:${item.scope ?? ""}`
  }
  return `${item.name}:${item.pending_version_id ?? ""}`
}

function DimensionHeader({ dimension, muted = false }: { dimension: UpdateDimension; muted?: boolean }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <h3 className={`text-sm font-medium ${muted ? "text-muted-foreground" : ""}`}>
        {dimension.label}
      </h3>
      <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
        {dimension.items.length}
      </span>
      {dimension.pending ? (
        <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-600">
          即将支持
        </span>
      ) : null}
    </div>
  )
}

function UpdateItemRow({
  item,
  renderedAt,
  onSelectSkill,
}: {
  item: UpdateItem
  renderedAt: number
  onSelectSkill?: (name: string) => void
}) {
  // A local-edit item carries device provenance instead of a pending upstream
  // version; render the two shapes differently so each shows what it has.
  const isLocal = !!item.device_id

  return (
    <li className="px-3 py-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <button
              type="button"
              className="min-w-0 rounded-sm text-left text-sm font-medium hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              onClick={() => onSelectSkill?.(item.name)}
            >
              <span className="break-words">{item.name}</span>
            </button>
            {item.url ? <UpdateLink url={item.url} /> : null}
            {isLocal ? (
              <span className="rounded bg-sky-500/15 px-1.5 py-0.5 text-[10px] font-medium text-sky-600">
                本地修改
              </span>
            ) : (
              <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-600">
                有更新
              </span>
            )}
          </div>
          {isLocal ? (
            <div className="text-muted-foreground mt-1 flex min-w-0 flex-wrap items-center gap-1.5 text-xs">
              <Laptop className="size-3.5 shrink-0" aria-hidden />
              <span className="font-mono">
                {item.device_name || item.device_id}
                {item.tool_key ? ` · ${item.tool_key}` : ""}
                {item.scope ? ` · ${item.scope}` : ""}
              </span>
            </div>
          ) : null}
        </div>
        <div className="text-muted-foreground flex shrink-0 flex-wrap items-center gap-2 text-xs sm:justify-end">
          {isLocal ? (
            <span className="font-mono">{(item.local_sha ?? "").slice(0, 12) || "无指纹"}</span>
          ) : (
            <>
              <span className="font-mono">{(item.pending_content_sha256 ?? "").slice(0, 12)}</span>
              <span>·</span>
              <span>{formatRelativeTime(item.pending_created_at, renderedAt, "未知时间")}</span>
            </>
          )}
        </div>
      </div>
    </li>
  )
}

function UpdateLink({ url }: { url: string }) {
  return (
    <a
      href={url}
      target="_blank"
      rel="noreferrer"
      className="text-muted-foreground hover:text-primary inline-flex items-center"
      aria-label="打开来源链接"
      onClick={(event) => event.stopPropagation()}
    >
      <ExternalLink className="size-3.5" aria-hidden />
    </a>
  )
}
