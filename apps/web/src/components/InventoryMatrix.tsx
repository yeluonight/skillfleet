import { useEffect, useState } from "react"

import { ChangeStateDialog } from "@/components/ChangeStateDialog"
import { api, supportedStatesForTool } from "@/lib/api"
import type { DeviceDrift, InventoryRun, InventorySkill, LocalState } from "@/lib/api"
import { LocalStateBadge, StateBadge } from "@/lib/status-meta"

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
  const [drift, setDrift] = useState<DeviceDrift | null>(null)
  // The skill whose State cell was clicked (drives ChangeStateDialog).
  // null = dialog closed.
  const [stateTarget, setStateTarget] = useState<InventorySkill | null>(null)

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

  if (run === undefined) {
    return <p className="text-muted-foreground mt-3 text-sm">Loading skills…</p>
  }
  if (run === null) {
    return (
      <p className="text-muted-foreground mt-3 text-sm">
        This device hasn't reported any skills yet. The agent uploads an inventory shortly after
        approval.
      </p>
    )
  }
  if (run.skills.length === 0) {
    return (
      <p className="text-muted-foreground mt-3 text-sm">
        Last scan found no skills across {run.root_count} root{run.root_count === 1 ? "" : "s"}.
      </p>
    )
  }

  // Group skills by tool, then scope, so the matrix reads
  // tool -> scope -> [skills]. The server already orders rows by
  // (tool, scope, name), so a single pass preserves order.
  const groups = groupSkills(run.skills)

  // Index drift by (tool, scope, name) so each matrix row can find its
  // local_state in O(1). Same key shape the rows render with.
  const driftByKey = new Map<string, LocalState>()
  if (drift) {
    for (const d of drift.skills) {
      driftByKey.set(`${d.tool_key}:${d.scope}:${d.name}`, d.local_state)
    }
  }

  return (
    <div className="mt-3 space-y-4">
      <p className="text-muted-foreground text-xs">
        {run.skill_count} skill{run.skill_count === 1 ? "" : "s"} across {run.root_count} root
        {run.root_count === 1 ? "" : "s"}
        {run.agent_version ? ` · agent ${run.agent_version}` : ""}
        {drift
          ? ` · ${drift.summary.local_modified} modified, ${drift.summary.untracked} untracked`
          : ""}
      </p>
      {groups.map((g) => (
        <div key={g.tool} className="rounded-md border">
          <div className="bg-muted/50 border-b px-3 py-1.5 text-xs font-semibold uppercase tracking-wide">
            {g.tool}
          </div>
          <table className="w-full text-sm">
            <thead>
              <tr className="text-muted-foreground border-b text-left text-xs">
                <th className="px-3 py-1.5 font-medium">Skill</th>
                <th className="px-3 py-1.5 font-medium">Scope</th>
                <th className="px-3 py-1.5 font-medium">State</th>
                <th className="px-3 py-1.5 font-medium">Local</th>
                <th className="px-3 py-1.5 font-medium">Native</th>
              </tr>
            </thead>
            <tbody>
              {g.skills.map((s) => (
                <tr key={`${s.tool_key}:${s.scope}:${s.name}`} className="border-b last:border-0">
                  <td className="px-3 py-1.5">
                    <div className="font-medium">{s.name}</div>
                    {s.description ? (
                      <div className="text-muted-foreground text-xs">{s.description}</div>
                    ) : null}
                    {s.warnings && s.warnings.length > 0 ? (
                      <div className="mt-0.5 text-xs text-amber-600">
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
                        title="点击更改启停状态"
                      >
                        <StateBadge state={s.effective_state} />
                      </button>
                    ) : (
                      <span title="该工具无原生启停信号，不可远程更改">
                        <StateBadge state={s.effective_state} />
                      </span>
                    )}
                  </td>
                  <td className="px-3 py-1.5">
                    <LocalStateBadge state={driftByKey.get(`${s.tool_key}:${s.scope}:${s.name}`)} />
                  </td>
                  <td className="text-muted-foreground px-3 py-1.5 font-mono text-xs">
                    {s.native_state ?? "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}

      {stateTarget ? (
        <ChangeStateDialog
          key={`${stateTarget.tool_key}:${stateTarget.scope}:${stateTarget.name}`}
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

type toolGroup = { tool: string; skills: InventorySkill[] }

function groupSkills(skills: InventorySkill[]): toolGroup[] {
  const groups: toolGroup[] = []
  let current: toolGroup | null = null
  for (const s of skills) {
    if (!current || current.tool !== s.tool_key) {
      current = { tool: s.tool_key, skills: [] }
      groups.push(current)
    }
    current.skills.push(s)
  }
  return groups
}
