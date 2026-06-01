import { useState } from "react"
import { Rocket, Send } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { JobsList } from "@/components/JobsList"
import { api, apiErrorMessage } from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"
import { useAsyncAction } from "@/hooks/useAsyncAction"
import type { Device, DeploymentJob, SkillVersion } from "@/lib/api"

// DeploySection owns the deploy-to-device flow + this skill's job list
// for one skill (phase 8). It is keyed by skill name at the call site so
// its selection/poll state resets on skill switch. Every deployment
// api.* call + CSRF + error handling lives here; JobsList is a pure
// controlled view. The server addresses install targets by
// {tool_key, scope}; the agent resolves them against its allowed roots.
export function DeploySection({
  skillName,
  versions,
}: {
  skillName: string
  versions: SkillVersion[]
}) {
  const [open, setOpen] = useState(false)
  const [versionId, setVersionId] = useState(versions[0]?.id ?? "")
  const [devices, setDevices] = useState<Device[]>([])
  const [deviceId, setDeviceId] = useState("")
  // Target options for the chosen device, derived from its inventory
  // (the tool_key + scope pairs the device actually reports).
  const [targets, setTargets] = useState<{ toolKey: string; scope: string }[]>([])
  const [targetIdx, setTargetIdx] = useState(0)
  const [actionError, setActionError] = useState<string | null>(null)
  const deployAction = useAsyncAction()

  // Per-row rollback busy (single-flight useAsyncAction doesn't fit a
  // per-job spinner).
  const [rollbackBusyId, setRollbackBusyId] = useState<string | null>(null)
  const [renderedAt, setRenderedAt] = useState(() => Date.now())

  // Job list: initial load on skill change + a steady 4s poll so a
  // claimed/running job is seen to settle without a manual refresh. The
  // fetcher stamps renderedAt on each success for relative-time display.
  const {
    data: jobsData,
    loading: jobsLoading,
    error: jobsError,
    refresh: refreshJobs,
    setError: setJobsError,
  } = useApiResource<{ jobs: DeploymentJob[] }>(
    async () => {
      const res = await api.listDeployments({ skill: skillName })
      setRenderedAt(Date.now())
      return res
    },
    { deps: [skillName], pollMs: 4000, errorFallback: "Failed to load deployment jobs." },
  )
  const jobs = jobsData?.jobs ?? []

  async function openDialog() {
    setActionError(null)
    setOpen(true)
    try {
      const res = await api.listDevices()
      const approved = res.devices.filter((d) => d.status === "approved")
      setDevices(approved)
      if (approved[0]) {
        setDeviceId(approved[0].id)
        void loadTargets(approved[0].id)
      }
    } catch (err) {
      setActionError(apiErrorMessage(err, "Failed to load devices."))
    }
  }

  async function loadTargets(id: string) {
    setTargetIdx(0)
    try {
      const res = await api.deviceInventory(id)
      // Dedup the device's reported (tool_key, scope) pairs into target options.
      const byKey = new Map<string, { toolKey: string; scope: string }>()
      for (const sk of res.run?.skills ?? []) {
        const key = `${sk.tool_key} ${sk.scope}`
        if (!byKey.has(key)) byKey.set(key, { toolKey: sk.tool_key, scope: sk.scope })
      }
      setTargets([...byKey.values()])
    } catch {
      setTargets([])
    }
  }

  function onDeviceChange(id: string) {
    setDeviceId(id)
    void loadTargets(id)
  }

  async function execute() {
    if (!versionId || !deviceId) {
      setActionError("Select a version and a device.")
      return
    }
    const target = targets[targetIdx]
    setActionError(null)
    const ok = await deployAction.run(
      () =>
        api.executeDeployment({
          skill_name: skillName,
          version_id: versionId,
          device_id: deviceId,
          tool_key: target?.toolKey,
          scope: target?.scope,
        }),
      "Failed to deploy.",
    )
    if (ok) {
      setOpen(false)
      await refreshJobs()
    }
  }

  async function rollback(jobId: string) {
    setRollbackBusyId(jobId)
    try {
      await api.rollbackDeployment(jobId)
      await refreshJobs()
    } catch (err) {
      setJobsError(apiErrorMessage(err, "Failed to roll back."))
    } finally {
      setRollbackBusyId(null)
    }
  }

  return (
    <div className="space-y-3 border-t pt-4">
      <div className="flex items-center justify-between gap-2">
        <div className="text-muted-foreground text-xs font-semibold uppercase tracking-wide">
          Deploy
        </div>
        <Button size="sm" variant="secondary" onClick={openDialog} disabled={versions.length === 0}>
          <Rocket className="size-4" aria-hidden />
          Deploy to device
        </Button>
      </div>

      <JobsList
        jobs={jobs}
        loading={jobsLoading}
        error={jobsError}
        onRefresh={() => refreshJobs()}
        onRollback={rollback}
        rollbackBusyId={rollbackBusyId}
        renderedAt={renderedAt}
      />

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>部署 {skillName}</DialogTitle>
            <DialogDescription>
              选择版本与目标设备，下发一个安装任务。设备上线后会拉取并安装。
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            <label className="block text-sm">
              <span className="text-muted-foreground mb-1 block text-xs">版本</span>
              <select
                className="bg-background h-9 w-full rounded-md border px-2 text-sm"
                value={versionId}
                onChange={(e) => setVersionId(e.target.value)}
              >
                {versions.map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.id} · {v.kind}
                    {v.version_label ? ` · ${v.version_label}` : ""}
                  </option>
                ))}
              </select>
            </label>

            <label className="block text-sm">
              <span className="text-muted-foreground mb-1 block text-xs">设备（仅已批准）</span>
              <select
                className="bg-background h-9 w-full rounded-md border px-2 text-sm"
                value={deviceId}
                onChange={(e) => onDeviceChange(e.target.value)}
              >
                {devices.length === 0 ? <option value="">无已批准设备</option> : null}
                {devices.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name} ({d.id})
                  </option>
                ))}
              </select>
            </label>

            <label className="block text-sm">
              <span className="text-muted-foreground mb-1 block text-xs">目标（工具 · scope）</span>
              <select
                className="bg-background h-9 w-full rounded-md border px-2 text-sm"
                value={targetIdx}
                onChange={(e) => setTargetIdx(Number(e.target.value))}
                disabled={targets.length === 0}
              >
                {targets.length === 0 ? (
                  <option value={0}>该设备无可用目标（需先上报 inventory）</option>
                ) : (
                  targets.map((t, i) => (
                    <option key={`${t.toolKey}-${t.scope}`} value={i}>
                      {t.toolKey} · {t.scope}
                    </option>
                  ))
                )}
              </select>
            </label>

            {actionError ?? deployAction.error ? (
              <p className="text-sm text-red-600">{actionError ?? deployAction.error}</p>
            ) : null}
          </div>

          <DialogFooter>
            <Button variant="ghost" onClick={() => setOpen(false)} disabled={deployAction.busy}>
              取消
            </Button>
            <Button
              onClick={execute}
              disabled={deployAction.busy || !versionId || !deviceId || targets.length === 0}
            >
              <Send className="size-4" aria-hidden />
              下发安装
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
