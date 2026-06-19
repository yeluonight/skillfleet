import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { FolderTree, Users } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { ChangeStateDialog } from "@/components/ChangeStateDialog"
import { ViewToggle } from "@/components/ViewToggle"
import { api, apiErrorMessage, supportedStatesForTool } from "@/lib/api"
import type { DeviceDrift, InventoryRun, InventorySkill, LocalState } from "@/lib/api"
import { LocalStateBadge, StateBadge } from "@/lib/status-meta"
import { groupByKey } from "@/lib/array"
import { cn } from "@/lib/utils"

// InventoryMatrix renders a device's latest inventory. The parent owns the
// inventory fetch and passes `run` down so the roots card and matrix share one
// request.
//
// Two views (优化改造 §3.3 + mgmt-refactor track E):
//   - "tool": skills grouped by tool_key, one table per tool (the default).
//   - "path": a master-detail layout — a left rail of filesystem roots split
//     into own vs shared (.agents) sections, and a right panel showing the
//     selected root's skills. This makes the cross-tool convention explicit:
//     which skills live in a tool's own root vs a shared root several tools
//     read, and (for a shared root) exactly which tools see each skill.
//
// Drift is a separate, best-effort fetch: if it fails the matrix still
// renders, just without the local_state badges.
export function InventoryMatrix({
  deviceId,
  run,
  onRefresh,
}: {
  deviceId: string
  run: InventoryRun | null | undefined
  onRefresh: () => void
}) {
  const { t } = useTranslation()
  const [drift, setDrift] = useState<DeviceDrift | null>(null)
  // The skill whose State cell was clicked (drives ChangeStateDialog).
  // null = dialog closed.
  const [stateTarget, setStateTarget] = useState<InventorySkill | null>(null)
  const [viewMode, setViewMode] = useState<"tool" | "path">("tool")
  // Registry skill names (best-effort): skills already in the registry are
  // managed and need no "import" affordance. null until loaded / on error.
  const [registryNames, setRegistryNames] = useState<Set<string> | null>(null)
  // Adoption in flight, keyed by tool:scope:name; and the last adopt error.
  const [adopting, setAdopting] = useState<string | null>(null)
  const [adoptError, setAdoptError] = useState<string | null>(null)

  // Drift is a separate, best-effort fetch: it must not block or fail the
  // matrix. A drift error simply leaves the badges absent.
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const res = await api.deviceDrift(deviceId)
        if (!cancelled) setDrift(res)
      } catch {
        if (!cancelled) setDrift(null)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [deviceId])

  // Registry skill names, best-effort: drives which discovered skills offer
  // an "import to registry" action (those not already managed).
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const res = await api.listSkills()
        if (!cancelled) setRegistryNames(new Set(res.skills.map((s) => s.name)))
      } catch {
        if (!cancelled) setRegistryNames(null)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [deviceId])

  async function adopt(s: InventorySkill) {
    const key = skillKey(s)
    setAdopting(key)
    setAdoptError(null)
    try {
      await api.adoptDeviceSkill(deviceId, s.name, { tool_key: s.tool_key, scope: s.scope })
      // Optimistically mark it managed so the button flips to a pending hint;
      // the capture job runs async on the agent's next poll.
      setRegistryNames((prev) => {
        const next = new Set(prev ?? [])
        next.add(s.name)
        return next
      })
    } catch (err) {
      setAdoptError(apiErrorMessage(err, t("devices.adoptFailed")))
    } finally {
      setAdopting(null)
    }
  }

  // Index drift by (tool, scope, name) so each row finds its local_state in
  // O(1). Same key shape the rows render with. Memoised so the path view's
  // child components get a stable map reference.
  const driftByKey = useMemo(() => {
    const m = new Map<string, LocalState>()
    if (drift) {
      for (const d of drift.skills) m.set(skillKey(d), d.local_state)
    }
    return m
  }, [drift])

  if (run === undefined) {
    return <p className="text-muted-foreground mt-3 text-sm">{t("devices.loadingSkills")}</p>
  }
  if (run === null) {
    return (
      <p className="text-muted-foreground mt-3 text-sm">{t("devices.noSkillsReported")}</p>
    )
  }
  if (run.skills.length === 0) {
    return (
      <p className="text-muted-foreground mt-3 text-sm">
        {t("devices.noSkillsFound", { count: run.root_count })}
      </p>
    )
  }

  // Shared row props threaded to whichever view renders the skill rows.
  const rowProps: SkillRowProps = {
    showTool: viewMode === "path",
    driftByKey,
    registryNames,
    adopting,
    onAdopt: (s) => void adopt(s),
    onChangeState: setStateTarget,
  }

  return (
    <div className="mt-3 space-y-4">
      <p className="text-muted-foreground text-xs">
        {t("devices.matrixSummary", { skills: run.skill_count, roots: run.root_count })}
        {run.agent_version ? t("devices.matrixAgent", { version: run.agent_version }) : ""}
        {drift
          ? t("devices.matrixDrift", {
              modified: drift.summary.local_modified,
              untracked: drift.summary.untracked,
            })
          : ""}
      </p>
      {adoptError ? <p className="text-state-danger-600 text-xs">{adoptError}</p> : null}
      <ViewToggle
        value={viewMode}
        onChange={setViewMode}
        label={t("devices.viewLabel")}
        options={[
          { value: "tool", label: t("devices.viewByTool") },
          { value: "path", label: t("devices.viewByPath") },
        ]}
      />

      {viewMode === "tool" ? (
        <ToolView skills={run.skills} rowProps={rowProps} />
      ) : (
        <PathMasterDetail skills={run.skills} rowProps={rowProps} />
      )}

      {stateTarget ? (
        <ChangeStateDialog
          key={skillKey(stateTarget)}
          open={stateTarget !== null}
          onOpenChange={(o) => {
            if (!o) setStateTarget(null)
          }}
          deviceId={deviceId}
          toolKey={stateTarget.tool_key}
          scope={stateTarget.scope}
          skillName={stateTarget.name}
          currentState={stateTarget.effective_state}
          onApplied={onRefresh}
        />
      ) : null}
    </div>
  )
}

// ToolView keeps the original per-tool grouping: one bordered table per
// tool_key. The server orders rows by (tool, scope, name), so a single
// groupByKey pass yields stable, contiguous groups.
function ToolView({ skills, rowProps }: { skills: InventorySkill[]; rowProps: SkillRowProps }) {
  const groups = groupByKey(skills, (s) => s.tool_key)
  return (
    <div className="space-y-4">
      {groups.map((g) => (
        <div key={g.key} className="rounded-md border">
          <div className="bg-muted/50 border-b px-3 py-1.5 text-xs font-semibold uppercase tracking-wide">
            {g.key}
          </div>
          <SkillTable skills={g.items} rowProps={rowProps} />
        </div>
      ))}
    </div>
  )
}

// rootEntry is one filesystem root in the master rail: its path, the tools
// that read it, its skills, whether it is shared, and a drift count.
type rootEntry = {
  key: string
  path: string
  shared: boolean
  tools: string[]
  skills: InventorySkill[]
  driftCount: number
}

// PathMasterDetail is the path-centric master-detail view. The left rail lists
// roots split into own vs shared sections; selecting one shows its skills on
// the right. Below md the two stack (rail above, detail below).
function PathMasterDetail({
  skills,
  rowProps,
}: {
  skills: InventorySkill[]
  rowProps: SkillRowProps
}) {
  const { t } = useTranslation()

  // Group by root_path (falling back to skill_path when an older agent didn't
  // report root_path), deriving each root's tools/shared/drift in one pass.
  const roots = useMemo<rootEntry[]>(() => {
    const groups = groupByKey(skills, (s) => s.root_path ?? s.skill_path)
    return groups.map((g) => {
      const tools = [...new Set(g.items.map((s) => s.tool_key))]
      const shared = g.items.some((s) => s.shared) || tools.length > 1
      const driftCount = g.items.filter(
        (s) => rowProps.driftByKey.get(skillKey(s)) === "local_modified",
      ).length
      return { key: g.key, path: g.key, shared, tools, skills: g.items, driftCount }
    })
  }, [skills, rowProps.driftByKey])

  const ownRoots = roots.filter((r) => !r.shared)
  const sharedRoots = roots.filter((r) => r.shared)

  // selectedKey may go stale when the device/run changes and the roots list
  // shifts; rather than reconcile it in an effect (cascading renders), resolve
  // the effective selection during render — fall back to the first root when
  // the stored key no longer matches. A null key means "use the first root".
  const [selectedKey, setSelectedKey] = useState<string | null>(null)
  const selected =
    roots.find((r) => r.key === selectedKey) ?? roots[0] ?? null

  return (
    <div className="grid gap-4 md:grid-cols-[minmax(13rem,18rem)_1fr]">
      {/* Master rail */}
      <div className="space-y-3">
        <RootSection
          title={t("devices.ownPaths")}
          icon={<FolderTree className="size-3.5" aria-hidden />}
          roots={ownRoots}
          selectedKey={selected?.key ?? ""}
          onSelect={setSelectedKey}
        />
        <RootSection
          title={t("devices.sharedPaths")}
          icon={<Users className="size-3.5" aria-hidden />}
          roots={sharedRoots}
          selectedKey={selected?.key ?? ""}
          onSelect={setSelectedKey}
        />
      </div>

      {/* Detail panel */}
      <div className="min-w-0">
        {selected ? (
          <div className="rounded-md border">
            <div className="bg-muted/50 border-b space-y-1 px-3 py-2">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-sm break-all">{selected.path}</span>
                {selected.shared ? (
                  <Badge variant="outline" className="border-state-info-500/30 bg-state-info-50 text-state-info-600">
                    {t("devices.sharedRoot")}
                  </Badge>
                ) : null}
              </div>
              {selected.shared ? (
                <p className="text-muted-foreground text-xs">
                  {t("devices.sharedByTools", {
                    count: selected.tools.length,
                    tools: selected.tools.join(" / "),
                  })}
                </p>
              ) : null}
            </div>
            <SkillTable skills={selected.skills} rowProps={rowProps} />
          </div>
        ) : (
          <p className="text-muted-foreground text-sm">{t("devices.selectRoot")}</p>
        )}
      </div>
    </div>
  )
}

// RootSection is one labelled group of roots in the master rail (own or
// shared). Each root is a selectable button showing its path, tool badges,
// skill count, and a drift dot when something changed locally.
function RootSection({
  title,
  icon,
  roots,
  selectedKey,
  onSelect,
}: {
  title: string
  icon: React.ReactNode
  roots: rootEntry[]
  selectedKey: string
  onSelect: (key: string) => void
}) {
  const { t } = useTranslation()
  if (roots.length === 0) return null
  return (
    <div className="space-y-1">
      <div className="text-muted-foreground flex items-center gap-1.5 px-1 text-xs font-semibold uppercase tracking-wide">
        {icon}
        {title}
      </div>
      <ul className="space-y-1">
        {roots.map((r) => {
          const isSel = r.key === selectedKey
          const base = r.path.split("/").filter(Boolean).slice(-2).join("/")
          return (
            <li key={r.key}>
              <button
                type="button"
                onClick={() => onSelect(r.key)}
                aria-pressed={isSel}
                className={cn(
                  "focus-visible:ring-ring/50 w-full rounded-md border px-2 py-1.5 text-left outline-none transition-colors focus-visible:ring-2",
                  isSel ? "border-primary bg-muted" : "hover:bg-muted/50 border-transparent",
                )}
                title={r.path}
              >
                <div className="truncate font-mono text-xs font-medium">…/{base}</div>
                <div className="mt-1 flex flex-wrap items-center gap-1">
                  {r.tools.map((tk) => (
                    <Badge key={tk} variant="secondary" className="px-1 py-0 text-[10px]">
                      {tk}
                    </Badge>
                  ))}
                </div>
                <div className="text-muted-foreground mt-1 flex items-center gap-2 text-[10px]">
                  <span>{t("devices.skillCount", { count: r.skills.length })}</span>
                  {r.driftCount > 0 ? (
                    <span className="text-state-warn-600 inline-flex items-center gap-0.5">
                      <span className="bg-state-warn-500 size-1.5 rounded-full" aria-hidden />
                      {t("devices.pathDriftCount", { count: r.driftCount })}
                    </span>
                  ) : null}
                </div>
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

// SkillRowProps bundles the per-row state + callbacks shared by both views, so
// the row renderer stays identical whether grouped by tool or by path.
type SkillRowProps = {
  showTool: boolean // show the owning tool under the skill name (path view)
  driftByKey: Map<string, LocalState>
  registryNames: Set<string> | null
  adopting: string | null
  onAdopt: (s: InventorySkill) => void
  onChangeState: (s: InventorySkill) => void
}

// SkillTable renders the skill detail rows shared by the tool view's per-tool
// tables and the path view's detail panel — one definition so the columns
// can't drift apart.
function SkillTable({ skills, rowProps }: { skills: InventorySkill[]; rowProps: SkillRowProps }) {
  const { t } = useTranslation()
  const { showTool, driftByKey, registryNames, adopting, onAdopt, onChangeState } = rowProps
  return (
    <table className="w-full text-sm">
      <thead>
        <tr className="text-muted-foreground border-b text-left text-xs">
          <th className="px-3 py-1.5 font-medium">{t("devices.colSkill")}</th>
          <th className="px-3 py-1.5 font-medium">{t("devices.colScope")}</th>
          <th className="px-3 py-1.5 font-medium">{t("devices.colState")}</th>
          <th className="px-3 py-1.5 font-medium">{t("devices.colLocal")}</th>
          <th className="px-3 py-1.5 font-medium">{t("devices.colNative")}</th>
          <th className="px-3 py-1.5 font-medium">{t("devices.colManage")}</th>
        </tr>
      </thead>
      <tbody>
        {skills.map((s) => (
          <tr key={skillKey(s)} className="border-b last:border-0">
            <td className="px-3 py-1.5">
              <div className="font-medium">{s.name}</div>
              {showTool ? (
                <div className="text-muted-foreground text-[10px] font-mono">{s.tool_key}</div>
              ) : null}
              {s.description ? (
                <div className="text-muted-foreground text-xs">{s.description}</div>
              ) : null}
              {s.warnings && s.warnings.length > 0 ? (
                <div className="text-state-warn-600 mt-0.5 text-xs">
                  {s.warnings.map((w) => w.code).join(", ")}
                </div>
              ) : null}
            </td>
            <td className="text-muted-foreground px-3 py-1.5 text-xs">{s.scope}</td>
            <td className="px-3 py-1.5">
              {supportedStatesForTool(s.tool_key).length > 0 ? (
                <button
                  type="button"
                  className="focus-visible:ring-ring/50 hover:bg-muted -mx-1 rounded px-1 py-0.5 text-left outline-none underline-offset-2 hover:underline focus-visible:ring-2"
                  onClick={() => onChangeState(s)}
                  title={t("devices.clickToChangeState")}
                >
                  <StateBadge state={s.effective_state} />
                </button>
              ) : (
                <span title={t("devices.noNativeStateSignal")}>
                  <StateBadge state={s.effective_state} />
                </span>
              )}
            </td>
            <td className="px-3 py-1.5">
              <LocalStateBadge state={driftByKey.get(skillKey(s))} />
            </td>
            <td className="text-muted-foreground px-3 py-1.5 font-mono text-xs">
              {s.native_state ?? "—"}
            </td>
            <td className="px-3 py-1.5">
              {registryNames === null ? null : registryNames.has(s.name) ? (
                <span className="text-muted-foreground text-xs">{t("devices.inRegistry")}</span>
              ) : (
                <Button
                  variant="secondary"
                  size="xs"
                  disabled={adopting !== null}
                  onClick={() => onAdopt(s)}
                  title={t("devices.adoptHint")}
                >
                  {adopting === skillKey(s) ? t("devices.adopting") : t("devices.adopt")}
                </Button>
              )}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

// skillKey is the stable (tool, scope, name) identity used for React keys,
// drift lookups, the adopt-in-flight marker, and the state-change dialog —
// one definition so those sites can't drift apart.
function skillKey(s: { tool_key: string; scope: string; name: string }): string {
  return `${s.tool_key}:${s.scope}:${s.name}`
}
