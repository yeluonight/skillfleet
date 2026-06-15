import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router-dom"
import {
  MonitorSmartphone,
  Boxes,
  FilePen,
  ArrowUpCircle,
  XCircle,
  ShieldAlert,
  ChevronRight,
  type LucideIcon,
} from "lucide-react"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { DeploymentStatusBadge, OperationBadge } from "@/lib/status-meta"
import { cn } from "@/lib/utils"
import { api } from "@/lib/api"
import type { DashboardResponse, DeploymentJob } from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"

// DashboardPage is the four-layer §13.8.2 overview: (1) six headline metric
// cards, (2) Top Action Items, (3) a risk radar + recent deployments, and
// (4) an inventory summary. It consumes /api/dashboard (t1) for the metrics +
// action items and a small recent-deployments fetch for layer 3. Every metric
// card and action routes to the page where the operator acts on it.
export function DashboardPage() {
  const { t } = useTranslation()
  const { data, loading, error } = useApiResource<DashboardResponse>(() => api.dashboard(), {
    errorFallback: "Failed to load dashboard.",
  })
  const { data: deploys } = useApiResource<{ jobs: DeploymentJob[] }>(
    () => api.listDeployments(),
    { errorFallback: "" },
  )

  if (error) {
    return (
      <div className="mx-auto max-w-6xl">
        <h1 className="text-2xl font-semibold tracking-tight">{t("nav.dashboard")}</h1>
        <Alert variant="destructive" className="mt-6">
          <ShieldAlert className="size-4" aria-hidden />
          <AlertTitle>{t("dashboard.loadFailed")}</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      </div>
    )
  }

  const metrics = data?.metrics
  const actions = data?.action_items ?? []

  return (
    <div className="mx-auto max-w-6xl space-y-8">
      <h1 className="text-2xl font-semibold tracking-tight">{t("nav.dashboard")}</h1>

      {/* Layer 1 — metric cards */}
      <MetricGrid metrics={metrics} loading={loading} />

      {/* Layer 2 — Top Action Items */}
      <ActionItems actions={actions} loading={loading} />

      {/* Layer 3 — risk radar + recent deployments */}
      <div className="grid gap-4 md:grid-cols-2">
        <RiskRadar metrics={metrics} loading={loading} />
        <RecentDeployments jobs={deploys?.jobs ?? null} />
      </div>

      {/* Layer 4 — inventory summary */}
      <MatrixSummary metrics={metrics} loading={loading} />
    </div>
  )
}

type MetricKey =
  | "onlineDevices"
  | "managedSkills"
  | "localEdits"
  | "upstreamUpdates"
  | "failedDeployments"
  | "highRiskItems"

const METRIC_DEFS: {
  key: MetricKey
  field: keyof NonNullable<DashboardResponse["metrics"]>
  icon: LucideIcon
  to: string
  danger?: boolean
}[] = [
  { key: "onlineDevices", field: "online_devices", icon: MonitorSmartphone, to: "/devices" },
  { key: "managedSkills", field: "managed_skills", icon: Boxes, to: "/skills" },
  { key: "localEdits", field: "local_edits", icon: FilePen, to: "/updates" },
  { key: "upstreamUpdates", field: "upstream_updates", icon: ArrowUpCircle, to: "/updates" },
  { key: "failedDeployments", field: "failed_deployments", icon: XCircle, to: "/deploys", danger: true },
  { key: "highRiskItems", field: "high_risk_items", icon: ShieldAlert, to: "/updates", danger: true },
]

function MetricGrid({
  metrics,
  loading,
}: {
  metrics: DashboardResponse["metrics"] | undefined
  loading: boolean
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  return (
    <div className="grid grid-cols-2 gap-4 lg:grid-cols-3">
      {METRIC_DEFS.map((def) => {
        const Icon = def.icon
        const value = metrics ? metrics[def.field] : undefined
        const danger = def.danger && (value ?? 0) > 0
        return (
          <Card
            key={def.key}
            onClick={() => navigate(def.to)}
            className="hover:border-ring cursor-pointer transition-colors"
          >
            <CardHeader className="flex flex-row items-center justify-between gap-2 pb-2">
              <CardTitle className="text-muted-foreground text-sm font-medium">
                {t(`dashboard.metric.${def.key}`)}
              </CardTitle>
              <Icon
                className={cn("size-4", danger ? "text-state-danger-600" : "text-muted-foreground")}
                aria-hidden
              />
            </CardHeader>
            <CardContent>
              {loading || value === undefined ? (
                <Skeleton className="h-8 w-12" />
              ) : (
                <div
                  className={cn(
                    "text-3xl font-semibold tabular-nums",
                    danger && "text-state-danger-600"
                  )}
                >
                  {value}
                </div>
              )}
              <p className="text-muted-foreground mt-1 text-xs">
                {t(`dashboard.metricHint.${def.key}`)}
              </p>
            </CardContent>
          </Card>
        )
      })}
    </div>
  )
}

// ACTION_ROUTES maps an action_item key to the page that resolves it.
const ACTION_ROUTES: Record<string, string> = {
  approve_devices: "/devices",
  resolve_conflicts: "/updates",
  review_upstream: "/updates",
  review_local_edits: "/updates",
  retry_failed: "/deploys",
  track_untracked: "/updates",
}

function ActionItems({
  actions,
  loading,
}: {
  actions: DashboardResponse["action_items"]
  loading: boolean
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">{t("dashboard.actionItemsTitle")}</CardTitle>
      </CardHeader>
      <CardContent>
        {loading ? (
          <div className="space-y-2">
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </div>
        ) : actions.length === 0 ? (
          <p className="text-muted-foreground text-sm">{t("dashboard.noActionItems")}</p>
        ) : (
          <ul className="divide-border divide-y">
            {actions.map((a) => {
              // Prefer our localised label keyed by action; fall back to the
              // server-supplied label for any future key the UI doesn't know.
              const label = t(`dashboard.action.${a.key}`, { defaultValue: a.label })
              const to = ACTION_ROUTES[a.key] ?? "/updates"
              return (
                <li key={a.key} className="flex items-center justify-between gap-3 py-2">
                  <div className="flex items-center gap-2">
                    <span className="bg-state-warn-50 text-state-warn-600 inline-flex min-w-6 items-center justify-center rounded px-1.5 text-xs font-semibold tabular-nums">
                      {a.count}
                    </span>
                    <span className="text-sm">{label}</span>
                  </div>
                  <Button variant="ghost" size="sm" onClick={() => navigate(to)}>
                    {t("dashboard.actionGo")}
                    <ChevronRight className="size-4" aria-hidden />
                  </Button>
                </li>
              )
            })}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function RiskRadar({
  metrics,
  loading,
}: {
  metrics: DashboardResponse["metrics"] | undefined
  loading: boolean
}) {
  const { t } = useTranslation()
  const rows = [
    { key: "riskUntracked", value: metrics?.untracked_skills ?? 0 },
    { key: "riskConflicts", value: metrics?.local_edits ?? 0 },
    { key: "riskFailed", value: metrics?.failed_deployments ?? 0 },
  ] as const
  const total = rows.reduce((n, r) => n + r.value, 0)
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">{t("dashboard.riskRadarTitle")}</CardTitle>
      </CardHeader>
      <CardContent>
        {loading ? (
          <Skeleton className="h-20 w-full" />
        ) : total === 0 ? (
          <p className="text-muted-foreground text-sm">{t("dashboard.noRisk")}</p>
        ) : (
          <ul className="space-y-2">
            {rows.map((r) => (
              <li key={r.key} className="flex items-center justify-between text-sm">
                <span className="text-muted-foreground">{t(`dashboard.${r.key}`)}</span>
                <span
                  className={cn(
                    "tabular-nums font-medium",
                    r.value > 0 ? "text-state-danger-600" : "text-muted-foreground"
                  )}
                >
                  {r.value}
                </span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function RecentDeployments({ jobs }: { jobs: DeploymentJob[] | null }) {
  const { t } = useTranslation()
  const recent = jobs?.slice(0, 5) ?? null
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">{t("dashboard.recentDeploysTitle")}</CardTitle>
      </CardHeader>
      <CardContent>
        {recent === null ? (
          <Skeleton className="h-20 w-full" />
        ) : recent.length === 0 ? (
          <p className="text-muted-foreground text-sm">{t("dashboard.noRecentDeploys")}</p>
        ) : (
          <ul className="space-y-2">
            {recent.map((j) => (
              <li key={j.id} className="flex items-center justify-between gap-2 text-sm">
                <span className="flex min-w-0 items-center gap-2">
                  <OperationBadge operation={j.operation} />
                  <span className="truncate">{j.skill_name ?? j.id}</span>
                </span>
                <DeploymentStatusBadge status={j.status} />
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function MatrixSummary({
  metrics,
  loading,
}: {
  metrics: DashboardResponse["metrics"] | undefined
  loading: boolean
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2">
        <CardTitle className="text-lg">{t("dashboard.matrixSummaryTitle")}</CardTitle>
        <Button variant="outline" size="sm" onClick={() => navigate("/devices")}>
          {t("dashboard.openDevices")}
          <ChevronRight className="size-4" aria-hidden />
        </Button>
      </CardHeader>
      <CardContent>
        {loading || !metrics ? (
          <Skeleton className="h-6 w-64" />
        ) : (
          <p className="text-muted-foreground text-sm">
            {t("dashboard.matrixSummaryBody", {
              skills: metrics.managed_skills,
              devices: metrics.online_devices,
            })}
          </p>
        )}
      </CardContent>
    </Card>
  )
}
