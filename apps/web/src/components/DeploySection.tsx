import { useEffect, useMemo, useState } from "react"
import { AlertCircle, Info, Rocket, Send } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
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
import type {
  DeployPlanHint,
  Device,
  DeploymentJob,
  InventorySkill,
  RootCandidate,
  SkillVersion,
} from "@/lib/api"

// DeploySection owns the deploy-to-device flow + this skill's job list
// for one skill (phase 8). It is keyed by skill name at the call site so
// its selection/poll state resets on skill switch. Every deployment
// api.* call + CSRF + error handling lives here; JobsList is a pure
// controlled view. The server addresses install targets by root_id when
// available, falling back to {tool_key, scope} for older inventory reports.
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
  const [targets, setTargets] = useState<DeployTarget[]>([])
  const [targetIdx, setTargetIdx] = useState(0)
  const [planHint, setPlanHint] = useState<DeployPlanHint | null>(null)
  const [planLoading, setPlanLoading] = useState(false)
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
  const target = targets[targetIdx]
  const canPreview = open && Boolean(versionId) && Boolean(deviceId) && Boolean(target)

  useEffect(() => {
    if (!canPreview || !target) {
      return
    }
    let cancelled = false
    ;(async () => {
      setPlanLoading(true)
      try {
        const res = await api.planDeployment(deployBody(skillName, versionId, deviceId, target))
        if (!cancelled) {
          setPlanHint(res.hint ?? null)
          setActionError(null)
        }
      } catch (err) {
        if (!cancelled) setActionError(apiErrorMessage(err, "Failed to preview deployment plan."))
      } finally {
        if (!cancelled) setPlanLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [canPreview, deviceId, skillName, target, versionId])

  async function openDialog() {
    setActionError(null)
    setPlanHint(null)
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
    setPlanHint(null)
    try {
      const res = await api.deviceInventory(id)
      setTargets(targetsFromInventory(res.run?.roots ?? [], res.run?.skills ?? []))
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
    setActionError(null)
    const ok = await deployAction.run(
      () => api.executeDeployment(deployBody(skillName, versionId, deviceId, target)),
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

  const sharedReaderNames = useMemo(
    () => planHint?.shared?.readers?.map((reader) => reader.name).join(", ") ?? "",
    [planHint],
  )

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
              <span className="text-muted-foreground mb-1 block text-xs">目标 root</span>
              <select
                className="bg-background h-9 w-full rounded-md border px-2 text-sm"
                value={targetIdx}
                onChange={(e) => setTargetIdx(Number(e.target.value))}
                disabled={targets.length === 0}
              >
                {targets.length === 0 ? (
                  <option value={0}>该设备无可用目标（需先上报 inventory 并注册 root）</option>
                ) : (
                  targets.map((t, i) => (
                    <option key={targetKey(t)} value={i}>
                      {t.toolKey} · {t.scope}
                      {t.shared ? " · shared" : ""}
                      {t.rootId ? ` · ${t.rootId}` : ""}
                    </option>
                  ))
                )}
              </select>
            </label>

            {target?.path ? (
              <p className="text-muted-foreground break-all font-mono text-xs">{target.path}</p>
            ) : null}

            {planHint?.shared ? (
              <Alert>
                <Info className="size-4" aria-hidden />
                <AlertTitle>Shared Agent Skills</AlertTitle>
                <AlertDescription className="space-y-1">
                  <p>
                    该目标读取 .agents/skills。安装到共享 root 后，读取该目录的工具会看到同一份内容
                    {sharedReaderNames ? `：${sharedReaderNames}` : "。"}
                  </p>
                  {planHint.shared.already_covered ? (
                    <p>
                      同名同内容已由共享 root {planHint.shared.covered_by_root_id ?? "agents"} 覆盖，通常无需再给该工具单独安装。
                    </p>
                  ) : null}
                </AlertDescription>
              </Alert>
            ) : target?.shared ? (
              <Alert>
                <Info className="size-4" aria-hidden />
                <AlertTitle>共享目录</AlertTitle>
                <AlertDescription>
                  这是 .agents/skills 共享 root；同一物理目录只需部署一次。
                </AlertDescription>
              </Alert>
            ) : null}

            {planLoading ? <p className="text-muted-foreground text-xs">正在预览部署计划…</p> : null}

            {actionError ?? deployAction.error ? (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden />
                <AlertTitle>部署失败</AlertTitle>
                <AlertDescription>{actionError ?? deployAction.error}</AlertDescription>
              </Alert>
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

type DeployTarget = {
  toolKey: string
  scope: string
  rootId?: string
  path?: string
  shared?: boolean
}

function targetsFromInventory(roots: RootCandidate[], skills: InventorySkill[]): DeployTarget[] {
  const registeredRoots = roots
    .filter((root) => root.registered && root.exists)
    .map((root) => ({
      toolKey: root.tool_key,
      scope: root.scope,
      rootId: root.root_id,
      path: root.path,
      shared: root.shared,
    }))
  if (registeredRoots.length > 0) return registeredRoots

  const byKey = new Map<string, DeployTarget>()
  for (const sk of skills) {
    const key = `${sk.tool_key} ${sk.scope}`
    if (!byKey.has(key)) byKey.set(key, { toolKey: sk.tool_key, scope: sk.scope })
  }
  return [...byKey.values()]
}

function deployBody(skillName: string, versionId: string, deviceId: string, target?: DeployTarget) {
  return {
    skill_name: skillName,
    version_id: versionId,
    device_id: deviceId,
    tool_key: target?.toolKey,
    scope: target?.scope,
    root_id: target?.rootId,
  }
}

function targetKey(target: DeployTarget) {
  return `${target.toolKey}:${target.scope}:${target.rootId ?? target.path ?? "fallback"}`
}
