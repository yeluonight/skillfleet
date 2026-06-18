import { useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
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
import { updateItemKey } from "@/lib/api"
import type { UpdateDimension, UpdateItem, UpdatesResponse, UpdatesSummary } from "@/lib/api"

export type UpdateActions = {
  onViewDiff?: (item: UpdateItem) => void
  onViewThreeWay?: (item: UpdateItem) => void
  onRecheck?: (item: UpdateItem) => void
  onCapture?: (item: UpdateItem) => void
  busyKey?: string | null
}

export type UpdatesCardProps = {
  data: UpdatesResponse | null
  error: string | null
  busy: boolean
  onRefresh: () => void
  onSelectSkill?: (name: string) => void
  actions?: UpdateActions
}

// UpdatesCard is a pure controlled surface for the Updates Page. Parents own
// API calls, CSRF, refresh state, errors, and any navigation on skill select.
export function UpdatesCard({ data, error, busy, onRefresh, onSelectSkill, actions }: UpdatesCardProps) {
  const { t } = useTranslation()
  const [renderedAt] = useState(() => Date.now())

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-3">
          <div className="space-y-1">
            <CardTitle className="flex items-center gap-2 text-lg">
              <Package className="text-primary size-5" aria-hidden />
              {t("updates.title")}
            </CardTitle>
            <CardDescription>{t("updates.desc")}</CardDescription>
          </div>
          <Button type="button" size="sm" variant="ghost" onClick={onRefresh} disabled={busy}>
            {busy ? (
              <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
              <RefreshCw className="size-4" aria-hidden />
            )}
            {t("common.refresh")}
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {error ? (
          <Alert variant="destructive">
            <AlertCircle className="size-4" aria-hidden />
            <AlertTitle>{t("updates.loadFailed")}</AlertTitle>
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
              actions={actions}
            />
          </>
        ) : !error ? (
          <p className="text-muted-foreground text-sm">{t("common.loading")}</p>
        ) : null}
      </CardContent>
    </Card>
  )
}

function SummaryCards({ summary }: { summary: UpdatesSummary }) {
  const { t } = useTranslation()
  return (
    <div className="grid gap-3 sm:grid-cols-3">
      <SummaryCard
        label={t("updates.summaryUpstream")}
        value={summary.upstream_updates}
        icon={<ArrowUpCircle className="size-5" aria-hidden />}
        emphasis={summary.upstream_updates > 0 ? "warn" : "muted"}
      />
      <SummaryCard
        label={t("updates.summaryLocalEdits")}
        value={summary.local_edits}
        icon={<Laptop className="size-5" aria-hidden />}
        emphasis={summary.local_edits > 0 ? "info" : "muted"}
      />
      <SummaryCard
        label={t("updates.summarySourceUnknown")}
        value={summary.source_unknown}
        icon={<AlertCircle className="size-5" aria-hidden />}
        emphasis={summary.source_unknown > 0 ? "danger" : "muted"}
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
  emphasis: "warn" | "muted" | "danger" | "info"
}) {
  const colour =
    emphasis === "warn"
      ? "text-state-warn-600"
      : emphasis === "danger"
        ? "text-state-danger-600"
        : emphasis === "info"
          ? "text-state-info-600"
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
  actions,
}: {
  dimensions: UpdateDimension[]
  renderedAt: number
  onSelectSkill?: (name: string) => void
  actions?: UpdateActions
}) {
  return (
    <div className="space-y-3">
      {dimensions.map((dimension) => (
        <DimensionSection
          key={dimension.key}
          dimension={dimension}
          renderedAt={renderedAt}
          onSelectSkill={onSelectSkill}
          actions={actions}
        />
      ))}
    </div>
  )
}

function DimensionSection({
  dimension,
  renderedAt,
  onSelectSkill,
  actions,
}: {
  dimension: UpdateDimension
  renderedAt: number
  onSelectSkill?: (name: string) => void
  actions?: UpdateActions
}) {
  const { t } = useTranslation()
  if (dimension.pending) {
    return (
      <section className="rounded-md border border-dashed bg-muted/30 px-3 py-3">
        <DimensionHeader dimension={dimension} muted />
        <div className="text-muted-foreground mt-2 flex items-center gap-2 text-sm">
          <Clock className="size-4 shrink-0" aria-hidden />
          <span>{t("updates.dimensionPending")}</span>
        </div>
      </section>
    )
  }

  return (
    <section className="rounded-md border px-3 py-3">
      <DimensionHeader dimension={dimension} />
      {dimension.items.length === 0 ? (
        <div className="text-muted-foreground mt-3 flex items-center gap-2 text-sm">
          <CheckCircle2 className="text-state-clean-600 size-4 shrink-0" aria-hidden />
          <span>{t("updates.noItems")}</span>
        </div>
      ) : (
        <ul className="divide-border mt-3 divide-y rounded-md border">
          {dimension.items.map((item) => (
            <UpdateItemRow
              key={updateItemKey(item)}
              item={item}
              renderedAt={renderedAt}
              onSelectSkill={onSelectSkill}
              actions={actions}
            />
          ))}
        </ul>
      )}
    </section>
  )
}

function DimensionHeader({ dimension, muted = false }: { dimension: UpdateDimension; muted?: boolean }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-wrap items-center gap-2">
      <h3 className={`text-sm font-medium ${muted ? "text-muted-foreground" : ""}`}>
        {dimension.label}
      </h3>
      <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
        {dimension.items.length}
      </span>
      {dimension.pending ? (
        <span className="bg-state-warn-50 text-state-warn-600 rounded px-1.5 py-0.5 text-[10px] font-medium">
          {t("updates.comingSoon")}
        </span>
      ) : null}
    </div>
  )
}

function UpdateItemRow({
  item,
  renderedAt,
  onSelectSkill,
  actions,
}: {
  item: UpdateItem
  renderedAt: number
  onSelectSkill?: (name: string) => void
  actions?: UpdateActions
}) {
  const { t } = useTranslation()
  // A local-edit item carries device provenance instead of a pending upstream
  // version; render the two shapes differently so each shows what it has.
  const isLocal = !!item.device_id
  const hasUpstream = !!item.pending_version_id
  const rowBusy = actions?.busyKey === updateItemKey(item)
  // Combined (local + upstream): a three-way diff is meaningful; pure upstream
  // gets a two-way diff; the device side of a combined/local item can be
  // captured back into the registry (Track A adoption).
  const showThreeWay = isLocal && hasUpstream && !!actions?.onViewThreeWay
  const showDiff = hasUpstream && !showThreeWay && !!actions?.onViewDiff
  const showRecheck = hasUpstream && !!actions?.onRecheck
  const showCapture = isLocal && !!actions?.onCapture

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
              <span className="bg-state-info-50 text-state-info-600 rounded px-1.5 py-0.5 text-[10px] font-medium">
                {t("updates.itemLocalEdit")}
              </span>
            ) : (
              <span className="bg-state-warn-50 text-state-warn-600 rounded px-1.5 py-0.5 text-[10px] font-medium">
                {t("updates.itemHasUpdate")}
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
        <div className="flex shrink-0 flex-col items-end gap-2">
          <div className="text-muted-foreground flex flex-wrap items-center gap-2 text-xs sm:justify-end">
            {isLocal ? (
              <span className="font-mono">{(item.local_sha ?? "").slice(0, 12) || t("updates.noFingerprint")}</span>
            ) : (
              <>
                <span className="font-mono">{(item.pending_content_sha256 ?? "").slice(0, 12)}</span>
                <span>·</span>
                <span>{formatRelativeTime(t, item.pending_created_at, renderedAt, t("updates.unknownTime"))}</span>
              </>
            )}
          </div>
          {showThreeWay || showDiff || showRecheck || showCapture ? (
            <div className="flex flex-wrap items-center gap-1.5">
              {showDiff ? (
                <Button variant="outline" size="xs" disabled={rowBusy} onClick={() => actions?.onViewDiff?.(item)}>
                  {t("updates.actionViewDiff")}
                </Button>
              ) : null}
              {showThreeWay ? (
                <Button variant="outline" size="xs" disabled={rowBusy} onClick={() => actions?.onViewThreeWay?.(item)}>
                  {t("updates.actionThreeWay")}
                </Button>
              ) : null}
              {showCapture ? (
                <Button variant="secondary" size="xs" disabled={rowBusy} onClick={() => actions?.onCapture?.(item)}>
                  {rowBusy ? t("updates.capturing") : t("updates.actionCapture")}
                </Button>
              ) : null}
              {showRecheck ? (
                <Button variant="ghost" size="xs" disabled={rowBusy} onClick={() => actions?.onRecheck?.(item)}>
                  {t("updates.actionRecheck")}
                </Button>
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
    </li>
  )
}

function UpdateLink({ url }: { url: string }) {
  const { t } = useTranslation()
  return (
    <a
      href={url}
      target="_blank"
      rel="noreferrer"
      className="text-muted-foreground hover:text-primary inline-flex items-center"
      aria-label={t("updates.openSourceLink")}
      onClick={(event) => event.stopPropagation()}
    >
      <ExternalLink className="size-3.5" aria-hidden />
    </a>
  )
}
