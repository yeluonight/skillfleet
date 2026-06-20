import { useState } from "react"
import { useTranslation } from "react-i18next"
import { ChevronDown, ChevronRight, Loader2, Rocket } from "lucide-react"
import {
  type FleetStatus,
  type FleetDeployment,
  type SkillVersion,
  api,
  apiErrorMessage,
  versionLabel,
} from "@/lib/api"
import { StateBadge, LocalStateBadge } from "@/lib/status-meta"
import { formatRelativeTime } from "@/lib/time"
import { groupByKey } from "@/lib/array"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ChangeStateDialog } from "@/components/ChangeStateDialog"
import { ViewToggle } from "@/components/ViewToggle"

// SkillFleetMatrix renders a skill's deployment footprint across devices
// (优化改造 §3.3 + §5.4 Step7). Each row = one device×tool×scope install,
// with effective_state + computed local_state badges, an active-job
// spinner, and an enable/disable action via ChangeStateDialog. Rollback and
// capture-drift are heavier flows left as follow-ups.
//
// Controlled view: the page owns the fleet-status fetch (shared with the
// skill Info Card) and passes the result down, so switching tabs back to the
// fleet view does not re-request and stays in sync with the summary.
export function SkillFleetMatrix({
  skillName,
  fleetStatus,
  fleetError,
  onRefresh,
  versions,
  currentVersionId,
  onDeployed,
}: {
  skillName: string
  fleetStatus: FleetStatus | null
  fleetError: string | null
  onRefresh: () => void
  // versions + currentVersionId back the one-click "deploy here" action on
  // not_deployed rows (a registered root the skill was never installed to):
  // the deploy targets the skill's current version, so the confirm dialog
  // can name it. onDeployed lets the page refresh the fleet view after.
  versions?: SkillVersion[]
  currentVersionId?: string
  onDeployed?: () => void
}) {
  const { t } = useTranslation()
  const [changeTarget, setChangeTarget] = useState<FleetDeployment | null>(null)
  const [viewMode, setViewMode] = useState<"tool" | "path">("tool")
  // Capture once for deterministic relative-time rendering (react-hooks
  // purity: no Date.now() inline in render).
  const [renderedAt] = useState(() => Date.now())
  // Deploy-here flow for not_deployed rows: clicking the row's "部署到此处"
  // button opens a confirm dialog keyed by that deployment; the real
  // executeDeployment call runs only after the operator confirms.
  const [deployTarget, setDeployTarget] = useState<FleetDeployment | null>(null)
  const [deployBusy, setDeployBusy] = useState(false)
  const [deployError, setDeployError] = useState<string | null>(null)

  async function confirmDeploy() {
    const target = deployTarget
    if (!target || !currentVersionId) return
    setDeployBusy(true)
    setDeployError(null)
    try {
      await api.executeDeployment({
        skill_name: skillName,
        version_id: currentVersionId,
        device_id: target.device_id,
        tool_key: target.tool_key,
        scope: target.scope,
        root_id: target.root_id || undefined,
      })
      setDeployTarget(null)
      onDeployed?.()
    } catch (err) {
      setDeployError(apiErrorMessage(err, t("skills.deployHereFailed")))
    } finally {
      setDeployBusy(false)
    }
  }

  // openDeploy clears a stale error from a prior row's failed deploy before
  // opening the dialog for a new target, so row B never inherits row A's error.
  function openDeploy(d: FleetDeployment) {
    setDeployError(null)
    setDeployTarget(d)
  }

  // The version the one-click "deploy here" targets (the skill's current
  // version), resolved to a label for the confirm dialog. Falls back to the
  // raw id when no versions were passed in.
  const currentVersion = versions?.find((v) => v.id === currentVersionId)
  const currentVersionLabel = currentVersion
    ? versionLabel(currentVersion)
    : (currentVersionId ?? "")

  if (fleetError) {
    return <p className="text-state-danger-600 text-sm">{fleetError}</p>
  }
  if (!fleetStatus) {
    return <p className="text-muted-foreground text-sm">{t("skills.loadingFleet")}</p>
  }
  if (fleetStatus.deployments.length === 0) {
    return (
      <p className="text-muted-foreground py-8 text-center text-sm">
        {t("skills.fleetEmpty")}
      </p>
    )
  }

  return (
    <div className="space-y-3">
      <ViewToggle
        value={viewMode}
        onChange={setViewMode}
        label={t("skills.fleetViewLabel")}
        options={[
          { value: "tool", label: t("skills.fleetViewByTool") },
          { value: "path", label: t("skills.fleetViewByPath") },
        ]}
      />
      {viewMode === "tool" ? (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-muted-foreground border-b text-left text-xs">
              <th className="py-2 pr-3 font-medium">{t("skills.fleetDevice")}</th>
              <th className="py-2 pr-3 font-medium">{t("skills.fleetTool")}</th>
              <th className="py-2 pr-3 font-medium">{t("skills.fleetScope")}</th>
              <th className="py-2 pr-3 font-medium">{t("skills.fleetState")}</th>
              <th className="py-2 pr-3 font-medium">{t("skills.fleetDrift")}</th>
              <th className="py-2 pr-3 font-medium">{t("skills.fleetTimes")}</th>
              <th className="py-2 pr-3 font-medium">{t("skills.fleetActions")}</th>
            </tr>
          </thead>
          <tbody>
            {fleetStatus.deployments.map((d) => (
              <tr key={`${d.device_id}/${d.tool_key}/${d.scope}`} className="border-b last:border-0">
                <td className="py-2 pr-3">
                  <div className="font-medium">{d.device_name}</div>
                  <div className="text-muted-foreground font-mono text-[10px]">{d.root_path}</div>
                </td>
                <td className="text-muted-foreground py-2 pr-3">{d.tool_key}</td>
                <td className="text-muted-foreground py-2 pr-3">{d.scope}</td>
                <td className="py-2 pr-3">
                  {d.local_state === "not_deployed" ? (
                    <span className="text-muted-foreground text-xs">—</span>
                  ) : (
                    <StateBadge state={d.effective_state} />
                  )}
                </td>
                <td className="py-2 pr-3">
                  <LocalStateBadge state={d.local_state} />
                </td>
                <td className="text-muted-foreground py-2 pr-3 text-xs">
                  <div title={t("skills.fleetEditedAt")}>
                    {t("skills.fleetEditedPrefix")}
                    {formatRelativeTime(t, d.modified_at, renderedAt, "—")}
                  </div>
                  {d.matched_version_created_at ? (
                    <div title={t("skills.fleetPublishedAt")}>
                      {t("skills.fleetPublishedPrefix")}
                      {formatRelativeTime(t, d.matched_version_created_at, renderedAt, "—")}
                    </div>
                  ) : null}
                </td>
                <td className="py-2 pr-3">
                  <div className="flex items-center gap-1">
                    {d.has_active_job ? (
                      <Loader2 className="size-4 animate-spin" aria-label={t("skills.fleetActiveJob")} />
                    ) : null}
                    {d.local_state === "not_deployed" ? (
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => openDeploy(d)}
                        disabled={!currentVersionId}
                        title={!currentVersionId ? t("skills.deployHereNoVersion") : undefined}
                      >
                        <Rocket className="size-4" aria-hidden />
                        {t("skills.deployHere")}
                      </Button>
                    ) : (
                      <Button variant="ghost" size="sm" onClick={() => setChangeTarget(d)}>
                        {d.effective_state === "on" ? t("skills.disable") : t("skills.enable")}
                      </Button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <PathRows
          deployments={fleetStatus.deployments}
          renderedAt={renderedAt}
          onChange={setChangeTarget}
          currentVersionId={currentVersionId}
          onDeploy={openDeploy}
        />
      )}

      {changeTarget && (
        <ChangeStateDialog
          open={true}
          onOpenChange={(v) => !v && setChangeTarget(null)}
          deviceId={changeTarget.device_id}
          toolKey={changeTarget.tool_key}
          scope={changeTarget.scope}
          skillName={skillName}
          currentState={changeTarget.effective_state}
          onApplied={onRefresh}
        />
      )}

      {deployTarget ? (
        <Dialog open={true} onOpenChange={(v) => !v && !deployBusy && setDeployTarget(null)}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{t("skills.deployHere")}</DialogTitle>
              <DialogDescription>
                {t("skills.deployHereConfirm", {
                  version: currentVersionLabel,
                  device: deployTarget.device_name,
                  path: deployTarget.root_path,
                })}
              </DialogDescription>
            </DialogHeader>
            {deployError ? (
              <p role="alert" className="text-state-danger-600 text-sm">{deployError}</p>
            ) : null}
            <DialogFooter>
              <Button variant="ghost" onClick={() => setDeployTarget(null)} disabled={deployBusy}>
                {t("common.cancel")}
              </Button>
              <Button onClick={confirmDeploy} disabled={deployBusy}>
                {deployBusy ? (
                  <Loader2 className="size-4 animate-spin" aria-hidden />
                ) : (
                  <Rocket className="size-4" aria-hidden />
                )}
                {t("skills.deployHere")}
              </Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      ) : null}
    </div>
  )
}

// PathRows renders one collapsible aggregate row per (device, root_path): the
// path-centric view. Collapsed, a row summarizes the path — device, the tools
// deployed there, an overall drift badge, and the newest edit time — so an
// operator scans paths without N scattered tool rows. Expanded, it lists each
// tool's effective + drift state, times, and enable/disable action. A shared
// path (e.g. ~/.agents/skills read by several tools) naturally collapses its
// multiple tool deployments onto one row.
function PathRows({
  deployments,
  renderedAt,
  onChange,
  currentVersionId,
  onDeploy,
}: {
  deployments: FleetDeployment[]
  renderedAt: number
  onChange: (d: FleetDeployment) => void
  currentVersionId?: string
  onDeploy: (d: FleetDeployment) => void
}) {
  const { t } = useTranslation()
  const groups = groupByKey(deployments, (d) => `${d.device_id} ${d.root_path}`)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())

  function toggle(key: string) {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  return (
    <div className="overflow-hidden rounded-md border">
      {groups.map((g) => {
        const first = g.items[0]
        // g.key (device_id + " " + root_path) contains spaces, which are
        // invalid in an HTML id — sanitize for the aria-controls/id pair so
        // the toggle button and its region stay a valid IDREF link.
        const regionId = `path-rows-${g.key.replace(/[^A-Za-z0-9_-]/g, "_")}`
        const isOpen = expanded.has(g.key)
        const Chevron = isOpen ? ChevronDown : ChevronRight
        const tools = [...new Set(g.items.map((d) => d.tool_key))]
        const driftCount = g.items.filter((d) => d.local_state === "local_modified").length
        const activeJobs = g.items.some((d) => d.has_active_job)
        // Newest edit across the tools sharing this path.
        const newestEdit = g.items.reduce<number | undefined>((acc, d) => {
          if (!d.modified_at) return acc
          return acc && acc > d.modified_at ? acc : d.modified_at
        }, undefined)
        return (
          <div key={g.key} className="border-b last:border-0">
            <button
              type="button"
              onClick={() => toggle(g.key)}
              aria-expanded={isOpen}
              aria-controls={regionId}
              className="hover:bg-muted/50 focus-visible:ring-ring/50 flex w-full items-start gap-2 rounded-none px-3 py-2 text-left outline-none transition-colors focus-visible:ring-2"
            >
              <Chevron className="text-muted-foreground mt-0.5 size-4 shrink-0" aria-hidden />
              <div className="min-w-0 flex-1 space-y-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{first.device_name}</span>
                  <span className="text-muted-foreground font-mono text-[11px] break-all">
                    {first.root_path}
                  </span>
                  {activeJobs ? (
                    <Loader2 className="size-3.5 animate-spin" aria-label={t("skills.fleetActiveJob")} />
                  ) : null}
                </div>
                <div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
                  <span>{t("skills.fleetToolCount", { count: tools.length, tools: tools.join(" / ") })}</span>
                  {driftCount > 0 ? (
                    <span className="text-state-warn-600">
                      {t("skills.fleetDriftCount", { count: driftCount })}
                    </span>
                  ) : null}
                  <span title={t("skills.fleetEditedAt")}>
                    {t("skills.fleetEditedPrefix")}
                    {formatRelativeTime(t, newestEdit, renderedAt, "—")}
                  </span>
                </div>
              </div>
            </button>

            {isOpen ? (
              <table id={regionId} className="w-full border-t text-sm">
                <thead>
                  <tr className="text-muted-foreground border-b text-left text-xs">
                    <th className="py-1.5 pr-3 pl-9 font-medium">{t("skills.fleetTool")}</th>
                    <th className="py-1.5 pr-3 font-medium">{t("skills.fleetScope")}</th>
                    <th className="py-1.5 pr-3 font-medium">{t("skills.fleetState")}</th>
                    <th className="py-1.5 pr-3 font-medium">{t("skills.fleetDrift")}</th>
                    <th className="py-1.5 pr-3 font-medium">{t("skills.fleetTimes")}</th>
                    <th className="py-1.5 pr-3 font-medium">{t("skills.fleetActions")}</th>
                  </tr>
                </thead>
                <tbody>
                  {g.items.map((d) => (
                    <tr key={`${d.tool_key}/${d.scope}`} className="border-b last:border-0">
                      <td className="py-1.5 pr-3 pl-9 font-medium">{d.tool_key}</td>
                      <td className="text-muted-foreground py-1.5 pr-3">{d.scope}</td>
                      <td className="py-1.5 pr-3">
                        {d.local_state === "not_deployed" ? (
                          <span className="text-muted-foreground text-xs">—</span>
                        ) : (
                          <StateBadge state={d.effective_state} />
                        )}
                      </td>
                      <td className="py-1.5 pr-3">
                        <LocalStateBadge state={d.local_state} />
                      </td>
                      <td className="text-muted-foreground py-1.5 pr-3 text-xs">
                        <div title={t("skills.fleetEditedAt")}>
                          {t("skills.fleetEditedPrefix")}
                          {formatRelativeTime(t, d.modified_at, renderedAt, "—")}
                        </div>
                        {d.matched_version_created_at ? (
                          <div title={t("skills.fleetPublishedAt")}>
                            {t("skills.fleetPublishedPrefix")}
                            {formatRelativeTime(t, d.matched_version_created_at, renderedAt, "—")}
                          </div>
                        ) : null}
                      </td>
                      <td className="py-1.5 pr-3">
                        {d.local_state === "not_deployed" ? (
                          <Button
                            variant="secondary"
                            size="xs"
                            onClick={() => onDeploy(d)}
                            disabled={!currentVersionId}
                            title={!currentVersionId ? t("skills.deployHereNoVersion") : undefined}
                          >
                            <Rocket className="size-3.5" aria-hidden />
                            {t("skills.deployHere")}
                          </Button>
                        ) : (
                          <Button variant="ghost" size="xs" onClick={() => onChange(d)}>
                            {d.effective_state === "on" ? t("skills.disable") : t("skills.enable")}
                          </Button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : null}
          </div>
        )
      })}
    </div>
  )
}
