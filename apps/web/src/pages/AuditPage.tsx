import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { useSearchParams } from "react-router-dom"
import type { ParseKeys } from "i18next"
import {
  User,
  Bot,
  Server,
  ChevronRight,
  ChevronDown,
  Copy,
  Loader2,
  ShieldAlert,
  SlidersHorizontal,
  RotateCcw,
  RefreshCw,
  FileDown,
  type LucideIcon,
} from "lucide-react"

import { Card, CardContent } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert"
import { api, apiErrorMessage } from "@/lib/api"
import type { AuditEntry } from "@/lib/api"
import { formatRelativeTime } from "@/lib/time"

// AuditPage (§13.8.17) renders the audit log as a reverse-chronological
// timeline with prefix/actor filters persisted to the URL query, expandable
// JSON detail, backward-cursor "load more" paging, and CSV/JSON export of the
// rows currently loaded. The list state lives in this component (not
// useApiResource) because paging appends and filter changes reset.

const PAGE_SIZE = 50

// ACTOR_OPTIONS drives the actor-type <Select>. "all" is the sentinel for "no
// actor filter" (Radix forbids an empty-string item value), mapped to an
// absent `actor` query param when applied.
const ACTOR_OPTIONS = ["all", "user", "agent", "system"] as const

const ACTOR_OPTION_LABEL: Record<string, ParseKeys> = {
  all: "audit.actorAll",
  user: "audit.actorTypeUser",
  agent: "audit.actorTypeAgent",
  system: "audit.actorTypeSystem",
}

// ACTOR_BADGE maps the three known actor types to a Lucide icon + semantic
// state token (user→info, agent→clean, system→muted). Unknown types fall back
// to the muted token with the raw string as label.
const ACTOR_BADGE: Record<string, { icon: LucideIcon; cls: string; labelKey: ParseKeys }> = {
  user: {
    icon: User,
    cls: "bg-state-info-50 text-state-info-600 border-state-info-500/30",
    labelKey: "audit.actorTypeUser",
  },
  agent: {
    icon: Bot,
    cls: "bg-state-clean-50 text-state-clean-600 border-state-clean-500/30",
    labelKey: "audit.actorTypeAgent",
  },
  system: {
    icon: Server,
    cls: "bg-state-muted-50 text-state-muted-600 border-state-muted-500/30",
    labelKey: "audit.actorTypeSystem",
  },
}

const FALLBACK_BADGE_CLS = "bg-state-muted-50 text-state-muted-600 border-state-muted-500/30"

type ListState = {
  entries: AuditEntry[]
  nextCursor?: number
  status: "loading" | "ready" | "error"
  error?: string
}

export function AuditPage() {
  const { t } = useTranslation()
  const [searchParams, setSearchParams] = useSearchParams()

  // Committed filters come from the URL so a refresh / shared link restores
  // them. The draft inputs seed from the URL once, then track edits until the
  // operator clicks "apply".
  const committedAction = searchParams.get("action") ?? ""
  const committedActor = searchParams.get("actor") ?? ""
  const [actionDraft, setActionDraft] = useState(committedAction)
  const [actorDraft, setActorDraft] = useState(committedActor || "all")

  const [list, setList] = useState<ListState>({ entries: [], status: "loading" })
  const [loadingMore, setLoadingMore] = useState(false)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  // Captured once so relative-time formatting stays pure during render
  // (Date.now() must not be called in render per react-hooks/purity).
  const [renderedAt] = useState(() => Date.now())

  // load fetches one page. `until === undefined` is the first page (replaces
  // the list + shows the skeleton); otherwise it's a backward-cursor page that
  // appends. State mutation lives here (a useCallback), never in the effect.
  const load = useCallback(
    async (until: number | undefined, token: { cancelled: boolean }) => {
      if (until === undefined) {
        setList({ entries: [], status: "loading" })
      } else {
        setLoadingMore(true)
      }
      try {
        const res = await api.listAudit({
          action: committedAction || undefined,
          actor: committedActor || undefined,
          until,
          limit: PAGE_SIZE,
        })
        if (token.cancelled) return
        setList((prev) => ({
          entries: until === undefined ? res.entries : [...prev.entries, ...res.entries],
          nextCursor: res.next_cursor,
          status: "ready",
        }))
      } catch (err) {
        if (token.cancelled) return
        setList((prev) => ({ ...prev, status: "error", error: apiErrorMessage(err, "") }))
      } finally {
        if (!token.cancelled) setLoadingMore(false)
      }
    },
    [committedAction, committedActor],
  )

  // tokenRef points at the live effect's cancel token so the manual refresh /
  // load-more handlers target the current lifecycle (mirrors useApiResource).
  const tokenRef = useRef({ cancelled: false })

  useEffect(() => {
    const token = { cancelled: false }
    tokenRef.current = token
    void load(undefined, token)
    return () => {
      token.cancelled = true
    }
  }, [load])

  const refresh = useCallback(() => {
    void load(undefined, tokenRef.current)
  }, [load])

  function applyFilters() {
    const next: Record<string, string> = {}
    const action = actionDraft.trim()
    if (action) next.action = action
    if (actorDraft && actorDraft !== "all") next.actor = actorDraft
    setSearchParams(next, { replace: true })
    setExpandedId(null)
  }

  function resetFilters() {
    setActionDraft("")
    setActorDraft("all")
    setSearchParams({}, { replace: true })
    setExpandedId(null)
  }

  function loadMore() {
    if (list.nextCursor !== undefined) void load(list.nextCursor, tokenRef.current)
  }

  function exportJson() {
    download(`audit-${Date.now()}.json`, JSON.stringify(list.entries, null, 2), "application/json")
  }

  function exportCsv() {
    const header = [
      "id",
      "created_at",
      "actor_type",
      "actor_id",
      "action",
      "target_type",
      "target_id",
      "detail",
    ]
    const rows = list.entries.map((e) =>
      [
        e.id,
        new Date(e.created_at).toISOString(),
        e.actor_type,
        e.actor_id ?? "",
        e.action,
        e.target_type ?? "",
        e.target_id ?? "",
        e.detail === undefined || e.detail === null ? "" : JSON.stringify(e.detail),
      ]
        .map(csvCell)
        .join(","),
    )
    download(`audit-${Date.now()}.csv`, [header.join(","), ...rows].join("\r\n"), "text/csv;charset=utf-8")
  }

  const hasEntries = list.entries.length > 0

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{t("audit.title")}</h1>
        <p className="text-muted-foreground mt-1 text-sm">{t("audit.description")}</p>
      </div>

      <Card>
        <CardContent className="flex flex-wrap items-end gap-3 pt-6">
          <div className="space-y-1">
            <label htmlFor="audit-action" className="text-muted-foreground text-xs font-medium">
              {t("audit.filterAction")}
            </label>
            <Input
              id="audit-action"
              value={actionDraft}
              onChange={(e) => setActionDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") applyFilters()
              }}
              placeholder={t("audit.filterActionPlaceholder")}
              className="w-56"
            />
          </div>
          <div className="space-y-1">
            <span className="text-muted-foreground block text-xs font-medium">
              {t("audit.filterActor")}
            </span>
            <Select value={actorDraft} onValueChange={setActorDraft}>
              <SelectTrigger className="w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ACTOR_OPTIONS.map((o) => (
                  <SelectItem key={o} value={o}>
                    {t(ACTOR_OPTION_LABEL[o])}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex gap-2">
            <Button onClick={applyFilters}>
              <SlidersHorizontal className="size-4" aria-hidden />
              {t("audit.apply")}
            </Button>
            <Button variant="outline" onClick={resetFilters}>
              <RotateCcw className="size-4" aria-hidden />
              {t("audit.reset")}
            </Button>
          </div>
          <div className="ml-auto flex gap-2">
            <Button variant="outline" size="sm" onClick={refresh}>
              <RefreshCw className="size-4" aria-hidden />
              {t("common.refresh")}
            </Button>
            <Button variant="outline" size="sm" onClick={exportCsv} disabled={!hasEntries}>
              <FileDown className="size-4" aria-hidden />
              {t("audit.exportCsv")}
            </Button>
            <Button variant="outline" size="sm" onClick={exportJson} disabled={!hasEntries}>
              <FileDown className="size-4" aria-hidden />
              {t("audit.exportJson")}
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardContent className="pt-6">
          {list.status === "loading" ? (
            <SkeletonList />
          ) : list.status === "error" ? (
            <Alert variant="destructive">
              <ShieldAlert className="size-4" aria-hidden />
              <AlertTitle>{t("audit.loadFailed")}</AlertTitle>
              {list.error ? <AlertDescription>{list.error}</AlertDescription> : null}
            </Alert>
          ) : !hasEntries ? (
            <p className="text-muted-foreground py-8 text-center text-sm">{t("audit.empty")}</p>
          ) : (
            <>
              <ol className="relative">
                {list.entries.map((e) => (
                  <AuditRow
                    key={e.id}
                    entry={e}
                    renderedAt={renderedAt}
                    expanded={expandedId === e.id}
                    onToggle={() => setExpandedId(expandedId === e.id ? null : e.id)}
                  />
                ))}
              </ol>
              {list.nextCursor !== undefined ? (
                <div className="mt-4 flex justify-center">
                  <Button variant="outline" onClick={loadMore} disabled={loadingMore}>
                    {loadingMore ? (
                      <>
                        <Loader2 className="size-4 animate-spin" aria-hidden />
                        {t("audit.loading")}
                      </>
                    ) : (
                      t("audit.loadMore")
                    )}
                  </Button>
                </div>
              ) : null}
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

function AuditRow({
  entry,
  renderedAt,
  expanded,
  onToggle,
}: {
  entry: AuditEntry
  renderedAt: number
  expanded: boolean
  onToggle: () => void
}) {
  const { t } = useTranslation()
  const target = [entry.target_type, entry.target_id].filter(Boolean).join("/")
  const Chevron = expanded ? ChevronDown : ChevronRight
  const iso = new Date(entry.created_at).toISOString()
  return (
    <li className="border-border relative border-l pb-3 pl-6 last:pb-0">
      <span
        className="bg-border ring-background absolute top-3 -left-[5px] size-2.5 rounded-full ring-4"
        aria-hidden
      />
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        className="hover:bg-accent/50 flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left transition-colors"
      >
        <div className="min-w-0 flex-1 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="sr-only">{t("audit.colActor")}:</span>
            <ActorBadge actorType={entry.actor_type} />
            {entry.actor_id ? (
              <code className="text-muted-foreground font-mono text-xs">{entry.actor_id}</code>
            ) : null}
            <span className="sr-only">{t("audit.colAction")}:</span>
            <code className="font-mono text-sm break-all">{entry.action}</code>
            {target ? (
              <span className="text-muted-foreground text-xs">
                <span className="sr-only">{t("audit.colTarget")}:</span>→ {target}
              </span>
            ) : null}
          </div>
          <div className="text-muted-foreground flex items-center gap-2 text-xs">
            <span className="sr-only">{t("audit.colTime")}:</span>
            <time dateTime={iso} title={new Date(entry.created_at).toLocaleString()}>
              {formatRelativeTime(entry.created_at, renderedAt, "")}
            </time>
          </div>
        </div>
        <Chevron className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden />
      </button>
      {expanded ? <DetailBlock entry={entry} /> : null}
    </li>
  )
}

function ActorBadge({ actorType }: { actorType: string }) {
  const { t } = useTranslation()
  const meta = ACTOR_BADGE[actorType]
  const Icon = meta?.icon ?? Server
  return (
    <Badge variant="outline" className={meta?.cls ?? FALLBACK_BADGE_CLS}>
      <Icon className="size-3" aria-hidden />
      {meta ? t(meta.labelKey) : actorType}
    </Badge>
  )
}

function DetailBlock({ entry }: { entry: AuditEntry }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)
  const hasDetail = entry.detail !== undefined && entry.detail !== null
  const json = hasDetail ? JSON.stringify(entry.detail, null, 2) : ""

  function copy() {
    navigator.clipboard?.writeText(json)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <div className="border-border bg-muted/40 mt-1 ml-2 space-y-2 rounded-md border p-3">
      <div className="flex items-center justify-between">
        <span className="text-muted-foreground text-xs font-medium">{t("audit.detail")}</span>
        {hasDetail ? (
          <Button variant="ghost" size="sm" onClick={copy}>
            <Copy className="size-3.5" aria-hidden />
            {copied ? t("common.copied") : t("common.copy")}
          </Button>
        ) : null}
      </div>
      {hasDetail ? (
        <pre className="text-foreground max-h-80 overflow-auto font-mono text-xs break-all whitespace-pre-wrap">
          {json}
        </pre>
      ) : (
        <p className="text-muted-foreground text-xs">{t("audit.noDetail")}</p>
      )}
    </div>
  )
}

function SkeletonList() {
  return (
    <div className="space-y-3">
      {[0, 1, 2, 3, 4].map((i) => (
        <div key={i} className="flex items-center gap-3">
          <Skeleton className="size-2.5 rounded-full" />
          <div className="flex-1 space-y-1.5">
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-3 w-1/3" />
          </div>
        </div>
      ))}
    </div>
  )
}

// csvCell quotes a field that contains a comma, quote, or newline, doubling
// any embedded quotes (RFC 4180).
function csvCell(value: string): string {
  return /[",\r\n]/.test(value) ? `"${value.replace(/"/g, '""')}"` : value
}

// download triggers a client-side file save via a transient object URL. Kept
// out of render (called from onClick) so URL.createObjectURL never runs during
// a render pass.
function download(filename: string, content: string, mime: string) {
  const blob = new Blob([content], { type: mime })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}
