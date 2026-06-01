import { useState, type ReactNode } from "react"
import { AlertCircle, Check, ExternalLink, GitBranch, GitCompare, RefreshCw, Unlink } from "lucide-react"

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
  const [renderedAt] = useState(() => Date.now())

  if (sourceState === "unbound") {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-lg">
            <GitBranch className="text-primary size-5" aria-hidden />
            来源绑定
          </CardTitle>
          <CardDescription>
            未绑定来源。绑定 GitHub 或 Git 仓库后，可在这里检查上游更新。
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button type="button" size="sm" onClick={onBind} disabled={busy}>
            <GitBranch className="size-4" aria-hidden />
            绑定来源
          </Button>
        </CardContent>
      </Card>
    )
  }

  if (!source) {
    return (
      <Alert variant="destructive">
        <AlertCircle className="size-4" aria-hidden />
        <AlertTitle>来源信息缺失</AlertTitle>
        <AlertDescription>当前 Skill 标记为已绑定，但没有可展示的来源详情。</AlertDescription>
      </Alert>
    )
  }

  const checkedAt = lastCheckedAt ?? source.last_checked_at
  const refLabel = formatRef(source.ref_type, source.ref_name)

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <CardTitle className="flex items-center gap-2 text-lg">
            <GitBranch className="text-primary size-5" aria-hidden />
            来源绑定
          </CardTitle>
          <SourceStateBadge state={sourceState} />
        </div>
        <CardDescription>此 Skill 已绑定上游来源；更新检查由父组件负责接线。</CardDescription>
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
            {source.subdir ? <span className="font-mono text-xs">{source.subdir}</span> : "仓库根"}
          </InfoRow>
          {source.last_remote_commit ? (
            <InfoRow label="last_remote_commit">
              <span className="font-mono text-xs">{source.last_remote_commit.slice(0, 12)}</span>
            </InfoRow>
          ) : null}
          <InfoRow label="last_checked_at">{formatRelativeTime(checkedAt, renderedAt, "从未")}</InfoRow>
        </dl>

        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" size="sm" onClick={onCheckUpdates} disabled={busy}>
            <RefreshCw className="size-4" aria-hidden />
            检查更新
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={onDetach} disabled={busy}>
            <Unlink className="size-4" aria-hidden />
            解绑
          </Button>
        </div>

        {lastCheck ? <CheckResultBanner result={lastCheck} onViewDiff={onViewDiff} /> : null}
      </CardContent>
    </Card>
  )
}

function SourceStateBadge({ state }: { state: SourceState }) {
  return (
    <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-emerald-500/15 text-emerald-600">
      {state}
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
  if (result.upstreamState === "up_to_date") {
    return (
      <div className="flex items-start gap-2 rounded-md border border-emerald-500/20 bg-emerald-500/10 px-2.5 py-2 text-sm text-emerald-700">
        <Check className="mt-0.5 size-4 shrink-0" aria-hidden />
        <span>已是最新</span>
      </div>
    )
  }

  if (result.upstreamState === "update_available") {
    return (
      <div className="flex flex-wrap items-center gap-2 rounded-md border border-amber-500/20 bg-amber-500/10 px-2.5 py-2 text-sm text-amber-700">
        <AlertCircle className="size-4 shrink-0" aria-hidden />
        <span className="flex-1">
          有可用更新
          {result.pendingVersionId ? `（待审版本 ${result.pendingVersionId.slice(0, 8)}）` : ""}
        </span>
        {onViewDiff ? (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="h-7 text-amber-700 hover:text-amber-800"
            onClick={onViewDiff}
          >
            <GitCompare className="size-3.5" aria-hidden />
            查看差异
          </Button>
        ) : null}
      </div>
    )
  }

  if (result.upstreamState === "remote_changed_no_skill_change") {
    return (
      <div className="text-muted-foreground flex items-start gap-2 rounded-md border border-border bg-muted/50 px-2.5 py-2 text-sm">
        <Check className="mt-0.5 size-4 shrink-0" aria-hidden />
        <span>远端有提交，但 Skill 内容未变；这不是更新。</span>
      </div>
    )
  }

  return (
    <div className="flex items-start gap-2 rounded-md border border-red-500/20 bg-red-500/10 px-2.5 py-2 text-sm text-red-700">
      <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden />
      <span>检查失败{result.error ? `：${result.error}` : ""}</span>
    </div>
  )
}

function formatRef(refType?: string, refName?: string): string | null {
  if (!refType && !refName) return null
  const kind = refType || "ref"
  const name = refName || (kind === "branch" ? "默认分支" : "未指定")
  return `${kind}:${name}`
}
