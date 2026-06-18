import {
  AlertCircle,
  CheckCircle2,
  Laptop,
  Loader2,
  RefreshCw,
  RotateCcw,
  Rocket,
} from "lucide-react"
import { useTranslation } from "react-i18next"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { formatRelativeTime } from "@/lib/time"
import { DeploymentStatusBadge, OperationBadge } from "@/lib/status-meta"
import type { DeploymentJob } from "@/lib/api"

export type JobsListProps = {
  jobs: DeploymentJob[]
  loading?: boolean
  error?: string | null
  onRefresh?: () => void
  // onRollback is offered only for a succeeded install job; clicking it
  // asks the parent to enqueue a rollback. The parent owns the API call.
  onRollback?: (jobId: string) => void
  rollbackBusyId?: string | null
  renderedAt?: number
}

// JobsList is a pure controlled surface for a skill's deployment jobs.
// Parents own loading, fetching, refresh, and the rollback action; this
// component only renders what it is given.
export function JobsList({
  jobs,
  loading = false,
  error = null,
  onRefresh,
  onRollback,
  rollbackBusyId = null,
  renderedAt,
}: JobsListProps) {
  const { t } = useTranslation()
  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 text-sm font-medium">
          <Rocket className="text-primary size-4" aria-hidden />
          {t("deploys.jobsTitle")}
        </div>
        {onRefresh ? (
          <Button type="button" size="sm" variant="ghost" onClick={onRefresh} disabled={loading}>
            {loading ? (
              <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
              <RefreshCw className="size-4" aria-hidden />
            )}
            {t("common.refresh")}
          </Button>
        ) : null}
      </div>

      {error ? (
        <Alert variant="destructive">
          <AlertCircle className="size-4" aria-hidden />
          <AlertTitle>{t("deploys.loadFailed")}</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      {jobs.length === 0 && !loading && !error ? (
        <div className="text-muted-foreground flex items-center gap-2 rounded-md border border-dashed px-3 py-4 text-sm">
          <CheckCircle2 className="text-state-clean-600 size-4 shrink-0" aria-hidden />
          <span>{t("deploys.noJobs")}</span>
        </div>
      ) : null}

      {jobs.length > 0 ? (
        <ul className="divide-border divide-y rounded-md border">
          {jobs.map((job) => (
            <JobRow
              key={job.id}
              job={job}
              renderedAt={renderedAt}
              onRollback={onRollback}
              rollbackBusy={rollbackBusyId === job.id}
            />
          ))}
        </ul>
      ) : null}
    </div>
  )
}

function JobRow({
  job,
  renderedAt,
  onRollback,
  rollbackBusy,
}: {
  job: DeploymentJob
  renderedAt?: number
  onRollback?: (jobId: string) => void
  rollbackBusy: boolean
}) {
  const { t } = useTranslation()
  const title = job.skill_name || job.version_id || "—"
  const canRollback =
    !!onRollback && job.operation === "install" && job.status === "succeeded"

  return (
    <li className="px-3 py-3">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <span className="break-words text-sm font-medium">{title}</span>
            <OperationBadge operation={job.operation} />
            <DeploymentStatusBadge status={job.status} />
            {job.rolled_back ? (
              <span className="bg-state-warn-50 text-state-warn-600 rounded px-1.5 py-0.5 text-[10px] font-medium">
                {t("deploys.rolledBack")}
              </span>
            ) : null}
          </div>
          <div className="text-muted-foreground mt-1 flex min-w-0 flex-wrap items-center gap-1.5 text-xs">
            <Laptop className="size-3.5 shrink-0" aria-hidden />
            <span className="font-mono">{job.device_id}</span>
            {renderedAt ? <span>· {formatRelativeTime(t, job.updated_at, renderedAt, "")}</span> : null}
          </div>
          {job.status === "failed" && job.error_message ? (
            <p className="text-state-danger-600 mt-1 text-xs">
              {job.error_code ? `${job.error_code}: ` : ""}
              {job.error_message}
            </p>
          ) : null}
        </div>
        {canRollback ? (
          <div className="shrink-0">
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => onRollback?.(job.id)}
              disabled={rollbackBusy}
            >
              {rollbackBusy ? (
                <Loader2 className="size-4 animate-spin" aria-hidden />
              ) : (
                <RotateCcw className="size-4" aria-hidden />
              )}
              {t("deploys.rollback")}
            </Button>
          </div>
        ) : null}
      </div>
    </li>
  )
}

