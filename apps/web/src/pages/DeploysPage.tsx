import { useState } from "react"
import { useTranslation } from "react-i18next"
import { AlertCircle, CheckCircle2, Loader2, RefreshCw, Rocket } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { DeploymentStatusBadge, OperationBadge } from "@/lib/status-meta"
import { formatRelativeTime } from "@/lib/time"
import { api } from "@/lib/api"
import type { DeploymentJob } from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"

// DeploysPage is the fleet-wide deployment-jobs route: every install /
// rollback / state_change job across all devices, newest activity polled on a
// steady interval. Per-skill rollback is a danger op that lives in
// DeploySection (the skill detail), so this global table is read-only — the
// page description points operators there to roll back.
export function DeploysPage() {
  const { t } = useTranslation()
  const [renderedAt, setRenderedAt] = useState(() => Date.now())
  const { data, loading, error, refresh } = useApiResource<{ jobs: DeploymentJob[] }>(
    async () => {
      const res = await api.listDeployments()
      setRenderedAt(Date.now())
      return res
    },
    { pollMs: 5000, errorFallback: t("deploys.err.loadJobs") },
  )
  const jobs = data?.jobs ?? []

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">{t("nav.deploys")}</h1>

      <Card>
        <CardHeader>
          <div className="flex items-start justify-between gap-3">
            <CardTitle className="flex items-center gap-2 text-lg">
              <Rocket className="text-primary size-5" aria-hidden />
              {t("deploys.jobsTitle")}
            </CardTitle>
            <Button type="button" size="sm" variant="ghost" onClick={() => void refresh()} disabled={loading}>
              {loading ? (
                <Loader2 className="size-4 animate-spin" aria-hidden />
              ) : (
                <RefreshCw className="size-4" aria-hidden />
              )}
              {t("common.refresh")}
            </Button>
          </div>
          <p className="text-muted-foreground text-sm">{t("deploys.pageDesc")}</p>
        </CardHeader>
        <CardContent>
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
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("deploys.colSkill")}</TableHead>
                  <TableHead>{t("deploys.colDevice")}</TableHead>
                  <TableHead>{t("deploys.colOperation")}</TableHead>
                  <TableHead>{t("deploys.colStatus")}</TableHead>
                  <TableHead>{t("deploys.colUpdated")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {jobs.map((job) => (
                  <TableRow key={job.id}>
                    <TableCell className="font-medium">
                      {job.skill_name || job.version_id || "—"}
                    </TableCell>
                    <TableCell className="text-muted-foreground font-mono text-xs">
                      {job.device_id}
                    </TableCell>
                    <TableCell>
                      <OperationBadge operation={job.operation} />
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-col gap-1">
                        <DeploymentStatusBadge status={job.status} />
                        {job.status === "failed" && job.error_message ? (
                          <span className="text-state-danger-600 text-xs">
                            {job.error_code ? `${job.error_code}: ` : ""}
                            {job.error_message}
                          </span>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs">
                      {formatRelativeTime(job.updated_at, renderedAt, "—")}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : null}
        </CardContent>
      </Card>
    </div>
  )
}
