import { useState, type ReactNode } from "react"
import { AlertCircle, Check, ExternalLink, GitBranch, GitCompare, RefreshCw, Unlink } from "lucide-react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { formatRelativeTime } from "@/lib/time"
import type { SourceState, SourceView, UpstreamState } from "@/lib/api"

export type SourceTabProps = {
  sourceState: SourceState
  source?: SourceView
  lastCheckedAt?: number
  lastCheck?: { upstreamState: UpstreamState; pendingVersionId?: string; error?: string } | null
  busy: boolean
  onBind: () => void
  onCheckUpdates: () => void
  onDetach: () => void
  // onViewDiff, when provided, surfaces a "view diff" affordance whenever an
  // upstream update is available (the parent owns loading the diff).
  onViewDiff?: () => void
}

// SourceTab is a pure controlled panel. It only renders bound source state
// and forwards user intent to parent-owned API/CSRF/error handling.
export function SourceTab({
  sourceState,
  source,
  lastCheckedAt,
  lastCheck,
  busy,
  onBind,
  onCheckUpdates,
  onDetach,
  onViewDiff,
}: SourceTabProps) {
  const { t } = useTranslation()
  const [renderedAt] = useState(() => Date.now())

  if (sourceState === "unbound") {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-lg">
            <GitBranch className="text-primary size-5" aria-hidden />
            {t("sources.bindingTitle")}
          </CardTitle>
          <CardDescription>
            {t("sources.unboundDesc")}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button type="button" size="sm" onClick={onBind} disabled={busy}>
            <GitBranch className="size-4" aria-hidden />
            {t("sources.bindSource")}
          </Button>
        </CardContent>
      </Card>
    )
  }

  if (!source) {
    return (
      <Alert variant="destructive">
        <AlertCircle className="size-4" aria-hidden />
        <AlertTitle>{t("sources.missingTitle")}</AlertTitle>
        <AlertDescription>{t("sources.missingDesc")}</AlertDescription>
      </Alert>
    )
  }

  const checkedAt = lastCheckedAt ?? source.last_checked_at
  const refLabel = formatRef(t, source.ref_type, source.ref_name)

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <CardTitle className="flex items-center gap-2 text-lg">
            <GitBranch className="text-primary size-5" aria-hidden />
            {t("sources.bindingTitle")}
          </CardTitle>
          <SourceStateBadge state={sourceState} />
        </div>
        <CardDescription>{t("sources.boundDesc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <dl className="grid gap-2 text-sm sm:grid-cols-[9rem_1fr]">
          <InfoRow label="URL">
            {source.url ? <SourceUrl url={source.url} /> : <span className="text-muted-foreground">—</span>}
          </InfoRow>
          {source.provider ? <InfoRow label="provider">{source.provider}</InfoRow> : null}
          {source.owner ? <InfoRow label="owner">{source.owner}</InfoRow> : null}
          {source.repo ? <InfoRow label="repo">{source.repo}</InfoRow> : null}
          {refLabel ? <InfoRow label="ref">{refLabel}</InfoRow> : null}
          <InfoRow label="subdir">
            {source.subdir ? <span className="font-mono text-xs">{source.subdir}</span> : t("sources.repoRoot")}
          </InfoRow>
          {source.last_remote_commit ? (
            <InfoRow label="last_remote_commit">
              <span className="font-mono text-xs">{source.last_remote_commit.slice(0, 12)}</span>
            </InfoRow>
          ) : null}
          <InfoRow label="last_checked_at">{formatRelativeTime(t, checkedAt, renderedAt, t("sources.never"))}</InfoRow>
        </dl>

        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" size="sm" onClick={onCheckUpdates} disabled={busy}>
            <RefreshCw className="size-4" aria-hidden />
            {t("sources.checkUpdates")}
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={onDetach} disabled={busy}>
            <Unlink className="size-4" aria-hidden />
            {t("sources.detach")}
          </Button>
        </div>

        {lastCheck ? <CheckResultBanner result={lastCheck} onViewDiff={onViewDiff} /> : null}
      </CardContent>
    </Card>
  )
}

function SourceStateBadge({ state }: { state: SourceState }) {
  const { t } = useTranslation()
  return (
    <span className="bg-state-clean-50 text-state-clean-600 border-state-clean-500/30 shrink-0 rounded border px-1.5 py-0.5 text-[10px] font-medium">
      {t(`sources.state.${state}`)}
    </span>
  )
}

function InfoRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground text-xs font-semibold uppercase tracking-wide">{label}</dt>
      <dd className="min-w-0 break-words text-sm">{children}</dd>
    </>
  )
}

function SourceUrl({ url }: { url: string }) {
  if (/^https?:\/\//i.test(url)) {
    return (
      <a
        href={url}
        target="_blank"
        rel="noreferrer"
        className="inline-flex max-w-full items-center gap-1 break-all text-primary hover:underline"
      >
        <span>{url}</span>
        <ExternalLink className="size-3.5 shrink-0" aria-hidden />
      </a>
    )
  }
  return <span className="font-mono text-xs break-all">{url}</span>
}

function CheckResultBanner({
  result,
  onViewDiff,
}: {
  result: { upstreamState: UpstreamState; pendingVersionId?: string; error?: string }
  onViewDiff?: () => void
}) {
  const { t } = useTranslation()
  if (result.upstreamState === "up_to_date") {
    return (
      <div className="bg-state-clean-50 text-state-clean-600 border-state-clean-500/30 flex items-start gap-2 rounded-md border px-2.5 py-2 text-sm">
        <Check className="mt-0.5 size-4 shrink-0" aria-hidden />
        <span>{t("sources.upToDate")}</span>
      </div>
    )
  }

  if (result.upstreamState === "update_available") {
    return (
      <div className="bg-state-warn-50 text-state-warn-600 border-state-warn-500/30 flex flex-wrap items-center gap-2 rounded-md border px-2.5 py-2 text-sm">
        <AlertCircle className="size-4 shrink-0" aria-hidden />
        <span className="flex-1">
          {t("sources.updateAvailable")}
          {result.pendingVersionId
            ? t("sources.pendingVersion", { id: result.pendingVersionId.slice(0, 8) })
            : ""}
        </span>
        {onViewDiff ? (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="text-state-warn-600 h-7"
            onClick={onViewDiff}
          >
            <GitCompare className="size-3.5" aria-hidden />
            {t("sources.viewDiff")}
          </Button>
        ) : null}
      </div>
    )
  }

  if (result.upstreamState === "remote_changed_no_skill_change") {
    return (
      <div className="text-muted-foreground flex items-start gap-2 rounded-md border border-border bg-muted/50 px-2.5 py-2 text-sm">
        <Check className="mt-0.5 size-4 shrink-0" aria-hidden />
        <span>{t("sources.remoteChangedNoSkillChange")}</span>
      </div>
    )
  }

  return (
    <div className="bg-state-danger-50 text-state-danger-600 border-state-danger-500/30 flex items-start gap-2 rounded-md border px-2.5 py-2 text-sm">
      <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden />
      <span>{result.error ? t("sources.checkFailedWithError", { error: result.error }) : t("sources.checkFailed")}</span>
    </div>
  )
}

function formatRef(t: TFunction, refType?: string, refName?: string): string | null {
  if (!refType && !refName) return null
  const kind = refType || "ref"
  const name = refName || (kind === "branch" ? t("sources.defaultBranch") : t("sources.unspecified"))
  return `${kind}:${name}`
}
