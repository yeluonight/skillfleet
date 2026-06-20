import { useId, useState, type FormEvent, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { AlertCircle, CheckCircle2, ChevronDown, ChevronRight, FolderCog, Plus, RefreshCw, Trash2 } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"

import { api, apiErrorMessage } from "@/lib/api"
import type { RootCandidate } from "@/lib/api"

const TOOL_OPTIONS = ["claude-code", "agents", "codex", "opencode", "antigravity", "antigravity-cli", "pi"]

// DeviceRootsCard renders the Phase 11 root-registration surface for one
// expanded device row. It receives run.roots from the parent so the roots card
// and skill matrix share one inventory fetch. Register/remove only enqueue
// jobs; the agent still validates and applies them on its next poll.
export function DeviceRootsCard({
  deviceId,
  roots,
  hasInventory,
  loading = false,
  onRefresh,
}: {
  deviceId: string
  roots: RootCandidate[]
  hasInventory: boolean
  loading?: boolean
  onRefresh: () => void
}) {
  const { t } = useTranslation()
  const [error, setError] = useState<string | null>(null)
  const [note, setNote] = useState<string | null>(null)
  const [busyKey, setBusyKey] = useState<string | null>(null)
  const [customTool, setCustomTool] = useState("claude-code")
  const [customScope, setCustomScope] = useState<"user" | "system">("user")
  const [customPath, setCustomPath] = useState("")

  async function register(root: RootCandidate) {
    setBusyKey(`register:${root.path}`)
    setError(null)
    setNote(null)
    try {
      await api.registerDeviceRoot(deviceId, {
        tool_key: root.tool_key,
        scope: root.scope,
        path: root.path,
      })
      setNote(t("devices.registerQueued"))
      onRefresh()
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to queue root registration."))
    } finally {
      setBusyKey(null)
    }
  }

  async function remove(root: RootCandidate) {
    if (!root.root_id) return
    setBusyKey(`remove:${root.root_id}`)
    setError(null)
    setNote(null)
    try {
      await api.removeDeviceRoot(deviceId, root.root_id)
      setNote(t("devices.removeQueued"))
      onRefresh()
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to queue root removal."))
    } finally {
      setBusyKey(null)
    }
  }

  async function registerCustom(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const path = customPath.trim()
    if (!path) {
      setError(t("devices.enterAbsolutePath"))
      return
    }
    setBusyKey("custom")
    setError(null)
    setNote(null)
    try {
      await api.registerDeviceRoot(deviceId, {
        tool_key: customTool,
        scope: customScope,
        path,
        custom: true,
      })
      setCustomPath("")
      setNote(t("devices.customQueued"))
      onRefresh()
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to queue custom root registration."))
    } finally {
      setBusyKey(null)
    }
  }

  const registered = roots.filter((root) => root.registered).length

  return (
    <section className="rounded-md border p-3">
      <RootsHeader onRefresh={onRefresh} />

      <div className="mt-3 space-y-3">
        {loading ? (
          <p className="text-muted-foreground text-sm">{t("devices.loadingRoots")}</p>
        ) : !hasInventory ? (
          <p className="text-muted-foreground text-sm">{t("devices.noInventoryForRoots")}</p>
        ) : (
          <div className="text-muted-foreground text-xs">
            {t("devices.rootsSummary", { total: roots.length, registered })}
          </div>
        )}

        {error ? (
          <Alert variant="destructive">
            <AlertCircle className="size-4" aria-hidden />
            <AlertTitle>{t("devices.rootActionFailed")}</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}

        {note ? (
          <Alert>
            <CheckCircle2 className="size-4" aria-hidden />
            <AlertTitle>{t("devices.jobQueued")}</AlertTitle>
            <AlertDescription>{note}</AlertDescription>
          </Alert>
        ) : null}

        {hasInventory && roots.length === 0 ? (
          <p className="text-muted-foreground text-sm">{t("devices.noCandidateRoots")}</p>
        ) : null}

        {roots.length > 0 ? (
          <div className="space-y-3">
            <RootGroup
              label={t("devices.ownPaths")}
              roots={roots.filter((r) => !r.shared)}
              busyKey={busyKey}
              onRegister={register}
              onRemove={remove}
            />
            <RootGroup
              label={t("devices.sharedPaths")}
              roots={roots.filter((r) => r.shared)}
              busyKey={busyKey}
              onRegister={register}
              onRemove={remove}
            />
          </div>
        ) : null}

        <form className="rounded-md border p-3" onSubmit={registerCustom}>
          <div className="mb-2 flex items-center gap-2 text-sm font-medium">
            <Plus className="text-primary size-4" aria-hidden />
            {t("devices.addCustomPath")}
          </div>
          <p className="text-muted-foreground mb-3 text-xs">{t("devices.customPathHint")}</p>
          <div className="grid gap-2 md:grid-cols-[minmax(0,1fr)_7rem_minmax(12rem,2fr)_auto]">
            <label className="text-xs">
              <span className="text-muted-foreground mb-1 block">{t("devices.tool")}</span>
              <select
                className="bg-background h-8 w-full rounded-lg border px-2 text-sm"
                value={customTool}
                onChange={(event) => setCustomTool(event.target.value)}
              >
                {TOOL_OPTIONS.map((tool) => (
                  <option key={tool} value={tool}>
                    {tool}
                  </option>
                ))}
              </select>
            </label>
            <label className="text-xs">
              <span className="text-muted-foreground mb-1 block">{t("devices.scope")}</span>
              <select
                className="bg-background h-8 w-full rounded-lg border px-2 text-sm"
                value={customScope}
                onChange={(event) => setCustomScope(event.target.value as "user" | "system")}
              >
                <option value="user">user</option>
                <option value="system">system</option>
              </select>
            </label>
            <label className="text-xs">
              <span className="text-muted-foreground mb-1 block">{t("devices.absolutePath")}</span>
              <Input
                value={customPath}
                onChange={(event) => setCustomPath(event.target.value)}
                placeholder="/home/me/custom/skills"
              />
            </label>
            <div className="flex items-end">
              <Button type="submit" size="sm" disabled={busyKey === "custom"}>
                {busyKey === "custom" ? t("devices.queueing") : t("devices.queue")}
              </Button>
            </div>
          </div>
        </form>
      </div>
    </section>
  )
}

function RootsHeader({ onRefresh }: { onRefresh: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="flex items-center justify-between gap-3">
      <div>
        <div className="flex items-center gap-2 text-sm font-semibold">
          <FolderCog className="text-primary size-4" aria-hidden />
          {t("devices.rootsTitle")}
        </div>
        <p className="text-muted-foreground text-xs">{t("devices.rootsDesc")}</p>
      </div>
      <Button type="button" variant="ghost" size="sm" onClick={onRefresh}>
        <RefreshCw className="size-4" aria-hidden />
        {t("common.refresh")}
      </Button>
    </div>
  )
}

// RootGroup renders one own/shared bucket of candidate roots. Existing
// paths are listed directly; missing paths (the directory isn't on the
// device yet) collapse into an "Other adoptable paths (N)" toggle so a
// device with many not-yet-created candidates stays scannable. The toggle
// is omitted entirely when there are no missing paths. Folding only tames
// noise — W2 lets the operator still register a missing path (the agent
// creates the dir), so nothing is hidden.
function RootGroup({
  label,
  roots,
  busyKey,
  onRegister,
  onRemove,
}: {
  label: string
  roots: RootCandidate[]
  busyKey: string | null
  onRegister: (root: RootCandidate) => void
  onRemove: (root: RootCandidate) => void
}) {
  const { t } = useTranslation()
  const [showMissing, setShowMissing] = useState(false)
  const missingId = useId()
  if (roots.length === 0) {
    return null
  }
  const existing = roots.filter((r) => r.exists)
  const missing = roots.filter((r) => !r.exists)
  const Chevron = showMissing ? ChevronDown : ChevronRight
  return (
    <div className="space-y-1.5">
      <div className="text-muted-foreground text-xs font-semibold uppercase tracking-wide">{label}</div>
      {existing.length > 0 ? (
        <div className="divide-border divide-y rounded-md border">
          {existing.map((root) => (
            <RootRow
              key={`${root.scope}:${root.tool_key}:${root.path}`}
              root={root}
              busyKey={busyKey}
              onRegister={() => onRegister(root)}
              onRemove={() => onRemove(root)}
            />
          ))}
        </div>
      ) : null}
      {missing.length > 0 ? (
        <div className="rounded-md border">
          <button
            type="button"
            onClick={() => setShowMissing((v) => !v)}
            aria-expanded={showMissing}
            aria-controls={missingId}
            className="hover:bg-muted/50 focus-visible:ring-ring/50 flex w-full items-center gap-2 rounded-none px-3 py-2 text-left text-sm outline-none focus-visible:ring-2"
          >
            <Chevron className="text-muted-foreground size-4 shrink-0" aria-hidden />
            <span className="text-muted-foreground">
              {t("devices.otherCandidates", { count: missing.length })}
            </span>
          </button>
          {showMissing ? (
            <div id={missingId} className="divide-border divide-y border-t">
              {missing.map((root) => (
                <RootRow
                  key={`${root.scope}:${root.tool_key}:${root.path}`}
                  root={root}
                  busyKey={busyKey}
                  onRegister={() => onRegister(root)}
                  onRemove={() => onRemove(root)}
                />
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

function RootRow({
  root,
  busyKey,
  onRegister,
  onRemove,
}: {
  root: RootCandidate
  busyKey: string | null
  onRegister: () => void
  onRemove: () => void
}) {
  const { t } = useTranslation()
  const registerBusy = busyKey === `register:${root.path}`
  const removeBusy = busyKey === `remove:${root.root_id}`
  return (
    <div className="flex flex-col gap-2 px-3 py-2 md:flex-row md:items-center md:justify-between">
      <div className="min-w-0 space-y-1">
        <div className="flex flex-wrap items-center gap-1.5 text-sm">
          <span className="font-medium">{root.tool_key}</span>
          <MetaPill>{root.scope}</MetaPill>
          {root.registered ? (
            <MetaPill>{t("devices.pill.registered")}</MetaPill>
          ) : (
            <MetaPill>{t("devices.pill.candidate")}</MetaPill>
          )}
          {root.shared ? <MetaPill>{t("devices.pill.shared")}</MetaPill> : null}
          {root.unconsumed ? <MetaPill>{t("devices.pill.unconsumed")}</MetaPill> : null}
          {root.exists ? (
            <MetaPill>{t("devices.pill.exists")}</MetaPill>
          ) : (
            <MetaPill>{t("devices.pill.missing")}</MetaPill>
          )}
          {root.tool_detected ? <MetaPill>{t("devices.pill.toolDetected")}</MetaPill> : null}
        </div>
        <div className="text-muted-foreground break-all font-mono text-xs">{root.path}</div>
        {root.display_tmpl ? (
          <div className="text-muted-foreground text-xs">
            {t("devices.template", { tmpl: root.display_tmpl })}
          </div>
        ) : null}
        {root.shared ? (
          <div className="text-muted-foreground text-xs">{t("devices.sharedRootHint")}</div>
        ) : null}
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {root.registered ? (
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={onRemove}
            disabled={removeBusy || !root.root_id}
          >
            <Trash2 className="size-4" aria-hidden />
            {removeBusy ? t("devices.queueing") : t("devices.remove")}
          </Button>
        ) : (
          <Button
            type="button"
            size="sm"
            onClick={onRegister}
            disabled={registerBusy}
            title={root.exists ? undefined : t("devices.registerWillCreateDir")}
          >
            {registerBusy ? t("devices.queueing") : t("devices.register")}
          </Button>
        )}
      </div>
    </div>
  )
}

function MetaPill({ children }: { children: ReactNode }) {
  return <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-[10px]">{children}</span>
}
