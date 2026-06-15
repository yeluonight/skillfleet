import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import type { TFunction } from "i18next"
import { AlertCircle, ChevronDown, ChevronRight, Cpu, RefreshCw } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { DeviceRootsCard } from "@/components/DeviceRootsCard"
import { InventoryMatrix } from "@/components/InventoryMatrix"

import { useApiResource } from "@/hooks/useApiResource"
import { api, apiErrorMessage } from "@/lib/api"
import type { Device, InventoryRun } from "@/lib/api"

// DevicesList renders the table of enrolled devices with approve /
// revoke actions. The server enforces the state machine; the UI just
// hides actions that would 4XX and shows a friendly post-action toast
// via inline status messages.
export function DevicesList() {
  const { t } = useTranslation()
  const [busyId, setBusyId] = useState<string | null>(null)
  const [renderedAt, setRenderedAt] = useState(() => Date.now())
  const {
    data: devicesData,
    error,
    refresh,
    setError,
  } = useApiResource<{ devices: Device[] }>(
    async () => {
      const res = await api.listDevices()
      setRenderedAt(Date.now())
      return res
    },
    { errorFallback: "Failed to load devices." },
  )
  const devices = devicesData?.devices ?? null

  // approve/revoke use a per-row busyId (not single-flight useAsyncAction)
  // so each row spins independently; errors route through the shared banner.
  async function approve(id: string) {
    setBusyId(id)
    try {
      await api.approveDevice(id)
      void refresh()
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to approve device."))
    } finally {
      setBusyId(null)
    }
  }

  async function revoke(id: string) {
    setBusyId(id)
    try {
      await api.revokeDevice(id)
      void refresh()
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to revoke device."))
    } finally {
      setBusyId(null)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg flex items-center gap-2">
          <Cpu className="text-primary size-5" aria-hidden />
          {t("devices.enrolledTitle")}
        </CardTitle>
        <CardDescription>{t("devices.enrolledDesc")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-center justify-end">
          <Button variant="ghost" size="sm" onClick={refresh}>
            <RefreshCw className="size-4" aria-hidden />
            {t("common.refresh")}
          </Button>
        </div>

        {error && (
          <Alert variant="destructive">
            <AlertCircle className="size-4" aria-hidden />
            <AlertTitle>{t("devices.deviceError")}</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <DeviceTable
          devices={devices}
          renderedAt={renderedAt}
          busyId={busyId}
          onApprove={approve}
          onRevoke={revoke}
        />
      </CardContent>
    </Card>
  )
}

function DeviceTable({
  devices,
  renderedAt,
  busyId,
  onApprove,
  onRevoke,
}: {
  devices: Device[] | null
  renderedAt: number
  busyId: string | null
  onApprove: (id: string) => void
  onRevoke: (id: string) => void
}) {
  const { t } = useTranslation()
  const [expandedId, setExpandedId] = useState<string | null>(null)

  if (devices === null) {
    return <p className="text-muted-foreground text-sm">{t("devices.loadingDevices")}</p>
  }
  if (devices.length === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        {t("devices.noDevicesPrefix")}
        <code className="font-mono text-xs">skillfleet-agent enroll</code>
        {t("devices.noDevicesSuffix")}
      </p>
    )
  }
  return (
    <ul className="divide-border divide-y rounded-md border">
      {devices.map((d) => {
        const expanded = expandedId === d.id
        return (
          <li key={d.id} className="px-3 py-3">
            <div className="flex items-center justify-between gap-4">
              <div className="space-y-0.5">
                <div className="text-sm font-medium">
                  {d.name}{" "}
                  {d.hostname && d.hostname !== d.name ? (
                    <span className="text-muted-foreground font-normal">({d.hostname})</span>
                  ) : null}
                </div>
                <div className="text-muted-foreground text-xs">
                  <StatusBadge status={d.status} />
                  <span className="ml-2">{describePlatform(d, t)}</span>
                  <span className="ml-2">
                    · {t("devices.lastSeen", { value: formatLastSeen(d.last_seen_at, renderedAt, t) })}
                  </span>
                </div>
                <div className="text-muted-foreground font-mono text-[10px]">{d.id}</div>
              </div>
              <div className="flex items-center gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setExpandedId(expanded ? null : d.id)}
                  aria-expanded={expanded}
                >
                  {expanded ? (
                    <ChevronDown className="size-4" aria-hidden />
                  ) : (
                    <ChevronRight className="size-4" aria-hidden />
                  )}
                  {t("devices.skills")}
                </Button>
                <DeviceActions
                  device={d}
                  busy={busyId === d.id}
                  onApprove={() => onApprove(d.id)}
                  onRevoke={() => onRevoke(d.id)}
                />
              </div>
            </div>
            {expanded ? <DeviceInventoryPanel deviceId={d.id} /> : null}
          </li>
        )
      })}
    </ul>
  )
}

function DeviceInventoryPanel({ deviceId }: { deviceId: string }) {
  const { t } = useTranslation()
  const [run, setRun] = useState<InventoryRun | null | undefined>(undefined)
  const [error, setError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const res = await api.deviceInventory(deviceId)
        if (!cancelled) {
          setError(null)
          setRun(res.run)
        }
      } catch (err) {
        if (!cancelled) setError(apiErrorMessage(err, "Failed to load inventory."))
      }
    })()
    return () => {
      cancelled = true
    }
  }, [deviceId, reloadKey])

  if (error) {
    return (
      <Alert variant="destructive" className="mt-3">
        <AlertCircle className="size-4" aria-hidden />
        <AlertTitle>{t("devices.inventoryError")}</AlertTitle>
        <AlertDescription>{error}</AlertDescription>
      </Alert>
    )
  }

  return (
    <div className="mt-3 space-y-4">
      <DeviceRootsCard
        deviceId={deviceId}
        roots={run?.roots ?? []}
        hasInventory={run !== null && run !== undefined}
        loading={run === undefined}
        onRefresh={() => setReloadKey((k) => k + 1)}
      />
      <InventoryMatrix
        deviceId={deviceId}
        run={run}
        onRefresh={() => setReloadKey((k) => k + 1)}
      />
    </div>
  )
}

function DeviceActions({
  device,
  busy,
  onApprove,
  onRevoke,
}: {
  device: Device
  busy: boolean
  onApprove: () => void
  onRevoke: () => void
}) {
  const { t } = useTranslation()
  if (device.status === "revoked") return null
  return (
    <div className="flex items-center gap-2">
      {device.status === "pending" && (
        <Button size="sm" onClick={onApprove} disabled={busy}>
          {busy ? t("devices.approving") : t("devices.approve")}
        </Button>
      )}
      <Button size="sm" variant="ghost" onClick={onRevoke} disabled={busy}>
        {busy ? t("devices.revoking") : t("devices.revoke")}
      </Button>
    </div>
  )
}

// StatusBadge colours the device lifecycle (approved/pending/revoked) with
// semantic state tokens; the label is localised.
function StatusBadge({ status }: { status: Device["status"] }) {
  const { t } = useTranslation()
  const colour =
    status === "approved"
      ? "text-state-clean-600"
      : status === "pending"
        ? "text-state-warn-600"
        : "text-muted-foreground"
  const label =
    status === "approved"
      ? t("devices.deviceStatus.approved")
      : status === "pending"
        ? t("devices.deviceStatus.pending")
        : t("devices.deviceStatus.revoked")
  return <span className={`font-medium uppercase tracking-wide ${colour}`}>{label}</span>
}

function describePlatform(d: Device, t: TFunction): string {
  const parts = [d.os, d.arch, d.agent_version].filter(Boolean)
  return parts.join(" / ") || t("devices.unknownPlatform")
}

// formatLastSeen returns "never", "5s ago", "2m ago", etc. Captures
// renderedAt at the call site (not inline Date.now()) to keep render
// deterministic for the react-hooks/purity rule.
function formatLastSeen(ts: number | undefined, renderedAt: number, t: TFunction): string {
  if (!ts) return t("devices.never")
  const delta = Math.max(0, renderedAt - ts)
  if (delta < 5_000) return t("devices.justNow")
  if (delta < 60_000) return t("devices.secondsAgo", { n: Math.floor(delta / 1000) })
  if (delta < 60 * 60_000) return t("devices.minutesAgo", { n: Math.floor(delta / 60_000) })
  if (delta < 24 * 60 * 60_000) return t("devices.hoursAgo", { n: Math.floor(delta / 3_600_000) })
  return new Date(ts).toLocaleString()
}
