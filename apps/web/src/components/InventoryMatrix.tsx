import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { ChangeStateDialog } from "@/components/ChangeStateDialog"
import { ViewToggle } from "@/components/ViewToggle"
import { api, apiErrorMessage, supportedStatesForTool } from "@/lib/api"
import type { DeviceDrift, InventoryRun, InventorySkill, LocalState } from "@/lib/api"
import { LocalStateBadge, StateBadge } from "@/lib/status-meta"
import { groupByKey } from "@/lib/array"

// InventoryMatrix renders a device's latest inventory as a tool x scope x
// skill grid. The parent owns the inventory fetch and passes `run` down so
// the roots card and matrix share one request.
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
  // View mode: group skills by tool (default) or by filesystem path. The path
  // view surfaces which skills come from a tool's own root vs a shared root
  // (e.g. ~/.agents/skills) — the cross-tool convention the tool view hides.
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

  // Group skills by the active view: by tool_key (default) or by root_path.
  // The server already orders rows by (tool, scope, name), so grouping is a
  // stable single pass. The group key doubles as the header label; the path
  // view also flags roots the agent marked shared across tools.
  const groups =
    viewMode === "tool"
      ? groupByKey(run.skills, (s) => s.tool_key)
      : groupByKey(run.skills, (s) => s.root_path ?? s.skill_path)

  // Index drift by (tool, scope, name) so each matrix row can find its
  // local_state in O(1). Same key shape the rows render with.
  const driftByKey = new Map<string, LocalState>()
  if (drift) {
    for (const d of drift.skills) {
      driftByKey.set(skillKey(d), d.local_state)
    }
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
      {groups.map((g) => {
        const shared = viewMode === "path" && g.items.some((s) => s.shared)
        return (
        <div key={g.key} className="rounded-md border">
          <div className="bg-muted/50 border-b flex flex-wrap items-center gap-2 px-3 py-1.5 text-xs font-semibold uppercase tracking-wide">
            <span className={viewMode === "path" ? "font-mono normal-case" : ""}>{g.key}</span>
            {shared ? (
              <span className="bg-state-info-100 text-state-info-700 rounded px-1.5 py-0.5 text-[10px] font-medium normal-case">
                {t("devices.sharedRoot")}
              </span>
            ) : null}
          </div>
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
              {g.items.map((s) => (
                <tr key={skillKey(s)} className="border-b last:border-0">
                  <td className="px-3 py-1.5">
                    <div className="font-medium">{s.name}</div>
                    {viewMode === "path" ? (
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
                        className="hover:bg-muted -mx-1 rounded px-1 py-0.5 text-left underline-offset-2 hover:underline"
                        onClick={() => setStateTarget(s)}
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
                        onClick={() => void adopt(s)}
                        title={t("devices.adoptHint")}
                      >
                        {adopting === skillKey(s)
                          ? t("devices.adopting")
                          : t("devices.adopt")}
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        )
      })}

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

// skillKey is the stable (tool, scope, name) identity used for React keys,
// drift lookups, the adopt-in-flight marker, and the state-change dialog —
// one definition so those sites can't drift apart.
function skillKey(s: { tool_key: string; scope: string; name: string }): string {
  return `${s.tool_key}:${s.scope}:${s.name}`
}
