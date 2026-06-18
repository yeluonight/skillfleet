import { useEffect, useMemo, useState } from "react"
import { AlertCircle, AlertTriangle, Info, Rocket, Send } from "lucide-react"
import { useTranslation } from "react-i18next"

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
  const { t } = useTranslation()
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
  // Rollback is a danger op (§13.8.1): clicking a row's rollback opens a
  // confirm dialog keyed by job id; the actual API call waits for confirm.
  const [rollbackConfirmId, setRollbackConfirmId] = useState<string | null>(null)
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
    { deps: [skillName], pollMs: 4000, errorFallback: t("deploys.err.loadJobs") },
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
        if (!cancelled) setActionError(apiErrorMessage(err, t("deploys.err.previewPlan")))
      } finally {
        if (!cancelled) setPlanLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [canPreview, deviceId, skillName, target, versionId, t])

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
      setActionError(apiErrorMessage(err, t("deploys.err.loadDevices")))
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
      setActionError(t("deploys.err.selectVersionDevice"))
      return
    }
    setActionError(null)
    const ok = await deployAction.run(
      () => api.executeDeployment(deployBody(skillName, versionId, deviceId, target)),
      t("deploys.err.deploy"),
    )
    if (ok) {
      setOpen(false)
      await refreshJobs()
    }
  }

  // Danger op: JobsList's rollback button only opens the confirm dialog;
  // the real API call runs in confirmRollback after the operator confirms.
  function requestRollback(jobId: string) {
    setRollbackConfirmId(jobId)
  }

  async function confirmRollback() {
    const jobId = rollbackConfirmId
    if (!jobId) return
    setRollbackConfirmId(null)
    setRollbackBusyId(jobId)
    try {
      await api.rollbackDeployment(jobId)
      await refreshJobs()
    } catch (err) {
      setJobsError(apiErrorMessage(err, t("deploys.err.rollback")))
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
          {t("deploys.sectionLabel")}
        </div>
        <Button size="sm" variant="secondary" onClick={openDialog} disabled={versions.length === 0}>
          <Rocket className="size-4" aria-hidden />
          {t("deploys.deployToDevice")}
        </Button>
      </div>

      <JobsList
        jobs={jobs}
        loading={jobsLoading}
        error={jobsError}
        onRefresh={() => refreshJobs()}
        onRollback={requestRollback}
        rollbackBusyId={rollbackBusyId}
        renderedAt={renderedAt}
      />

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t("deploys.dialogTitle", { name: skillName })}</DialogTitle>
            <DialogDescription>
              {t("deploys.dialogDesc")}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            <label className="block text-sm">
              <span className="text-muted-foreground mb-1 block text-xs">{t("deploys.version")}</span>
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
              <span className="text-muted-foreground mb-1 block text-xs">{t("deploys.deviceApprovedOnly")}</span>
              <select
                className="bg-background h-9 w-full rounded-md border px-2 text-sm"
                value={deviceId}
                onChange={(e) => onDeviceChange(e.target.value)}
              >
                {devices.length === 0 ? <option value="">{t("deploys.noApprovedDevice")}</option> : null}
                {devices.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name} ({d.id})
                  </option>
                ))}
              </select>
            </label>

            <label className="block text-sm">
              <span className="text-muted-foreground mb-1 block text-xs">{t("deploys.targetRoot")}</span>
              <select
                className="bg-background h-9 w-full rounded-md border px-2 text-sm"
                value={targetIdx}
                onChange={(e) => setTargetIdx(Number(e.target.value))}
                disabled={targets.length === 0}
              >
                {targets.length === 0 ? (
                  <option value={0}>{t("deploys.noTargetAvailable")}</option>
                ) : (
                  targets.map((tg, i) => (
                    <option key={targetKey(tg)} value={i}>
                      {tg.toolKey} · {tg.scope}
                      {tg.shared ? " · shared" : ""}
                      {tg.rootId ? ` · ${tg.rootId}` : ""}
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
                <AlertTitle>{t("deploys.sharedAgentSkills")}</AlertTitle>
                <AlertDescription className="space-y-1">
                  <p>
                    {t("deploys.sharedReadHint")}
                    {sharedReaderNames ? `：${sharedReaderNames}` : "。"}
                  </p>
                  {planHint.shared.already_covered ? (
                    <p>
                      {t("deploys.sharedAlreadyCovered", {
                        root: planHint.shared.covered_by_root_id ?? "agents",
                      })}
                    </p>
                  ) : null}
                </AlertDescription>
              </Alert>
            ) : target?.shared ? (
              <Alert>
                <Info className="size-4" aria-hidden />
                <AlertTitle>{t("deploys.sharedDir")}</AlertTitle>
                <AlertDescription>
                  {t("deploys.sharedDirDesc")}
                </AlertDescription>
              </Alert>
            ) : null}

            {planLoading ? <p className="text-muted-foreground text-xs">{t("deploys.previewingPlan")}</p> : null}

            {actionError ?? deployAction.error ? (
              <Alert variant="destructive">
                <AlertCircle className="size-4" aria-hidden />
                <AlertTitle>{t("deploys.deployFailed")}</AlertTitle>
                <AlertDescription>{actionError ?? deployAction.error}</AlertDescription>
              </Alert>
            ) : null}
          </div>

          <DialogFooter>
            <Button variant="ghost" onClick={() => setOpen(false)} disabled={deployAction.busy}>
              {t("common.cancel")}
            </Button>
            <Button
              onClick={execute}
              disabled={deployAction.busy || !versionId || !deviceId || targets.length === 0}
            >
              <Send className="size-4" aria-hidden />
              {t("deploys.sendInstall")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Rollback confirm (§13.8.1 danger op): the JobsList button only opens
          this; the real api.rollbackDeployment runs after the operator confirms. */}
      <Dialog open={rollbackConfirmId !== null} onOpenChange={(o) => !o && setRollbackConfirmId(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <AlertTriangle className="text-state-danger-600 size-5" aria-hidden />
              {t("deploys.rollbackConfirmTitle")}
            </DialogTitle>
            <DialogDescription>{t("deploys.rollbackConfirmDesc")}</DialogDescription>
          </DialogHeader>
          <p className="text-muted-foreground text-xs">{t("deploys.auditNote")}</p>
          <DialogFooter>
            <Button variant="ghost" onClick={() => setRollbackConfirmId(null)}>
              {t("common.cancel")}
            </Button>
            <Button variant="destructive" onClick={confirmRollback}>
              {t("deploys.rollback")}
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
