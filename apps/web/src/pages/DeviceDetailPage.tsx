import { useState } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { ArrowLeft } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useApiResource } from "@/hooks/useApiResource"
import { api, apiErrorMessage } from "@/lib/api"
import type { Device, InventoryRun, DeploymentJob } from "@/lib/api"
import { InventoryMatrix } from "@/components/InventoryMatrix"
import { DeviceRootsCard } from "@/components/DeviceRootsCard"
import { JobsList } from "@/components/JobsList"
import { DeviceActions } from "@/components/DeviceActions"

// DeviceDetailPage is the per-device workspace (优化改造 §5.4 Step4). Three
// tabs — Skills (inventory matrix), Roots, and deploy jobs — each backed by
// its own useApiResource so they load independently. approve/revoke live in
// the header.
export function DeviceDetailPage() {
  const { t } = useTranslation()
  const { id = "" } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [actionBusy, setActionBusy] = useState(false)

  const { data: device, refresh: refreshDevice } = useApiResource<Device>(
    () => api.getDevice(id),
    { deps: [id] },
  )
  const {
    data: inv,
    error: invError,
    refresh: refreshInv,
  } = useApiResource<{ run: InventoryRun | null }>(() => api.deviceInventory(id), { deps: [id] })
  const {
    data: jobsData,
    error: jobsError,
    refresh: refreshJobs,
  } = useApiResource<{ jobs: DeploymentJob[] }>(() => api.listDeployments({ device: id }), {
    deps: [id],
    pollMs: 5000,
  })

  async function runDeviceAction(fn: () => Promise<unknown>) {
    setActionBusy(true)
    try {
      await fn()
      refreshDevice()
    } finally {
      setActionBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" onClick={() => navigate("/devices")}>
          <ArrowLeft className="size-4" aria-hidden />
          {t("devices.backToDevices")}
        </Button>
        <h1 className="text-xl font-semibold">{device?.name ?? id}</h1>
        {device && (
          <DeviceActions
            device={device}
            busy={actionBusy}
            onApprove={() => runDeviceAction(() => api.approveDevice(device.id))}
            onRevoke={() => runDeviceAction(() => api.revokeDevice(device.id))}
          />
        )}
      </div>

      <Tabs defaultValue="skills" className="w-full">
        <TabsList>
          <TabsTrigger value="skills">{t("devices.tabs.skills")}</TabsTrigger>
          <TabsTrigger value="roots">{t("devices.tabs.roots")}</TabsTrigger>
          <TabsTrigger value="jobs">{t("devices.tabs.jobs")}</TabsTrigger>
        </TabsList>

        <TabsContent value="skills">
          <InventoryMatrix deviceId={id} run={inv?.run} onRefresh={refreshInv} />
        </TabsContent>
        <TabsContent value="roots">
          <DeviceRootsCard
            deviceId={id}
            roots={inv?.run?.roots ?? []}
            hasInventory={inv?.run != null}
            loading={inv === null && !invError}
            onRefresh={refreshInv}
          />
        </TabsContent>
        <TabsContent value="jobs">
          <JobsList
            jobs={jobsData?.jobs ?? []}
            loading={jobsData === null}
            error={jobsError ? apiErrorMessage(jobsError, "") : null}
            onRefresh={refreshJobs}
          />
        </TabsContent>
      </Tabs>
    </div>
  )
}
