import { useState } from "react"
import { AlertCircle, ChevronDown, ChevronRight, Cpu, RefreshCw } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { InventoryMatrix } from "@/components/InventoryMatrix"

import { api, apiErrorMessage } from "@/lib/api"
import type { Device } from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"

// DevicesList renders the table of enrolled devices with approve /
// revoke actions. The server enforces the state machine; the UI just
// hides actions that would 4XX and shows a friendly post-action toast
// via inline status messages.
export function DevicesList() {
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
          Enrolled devices
        </CardTitle>
        <CardDescription>
          Approve pending agents so they can heartbeat and ship inventory; revoke any that
          shouldn't be talking to this server anymore.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-center justify-end">
          <Button variant="ghost" size="sm" onClick={refresh}>
            <RefreshCw className="size-4" aria-hidden />
            Refresh
          </Button>
        </div>

        {error && (
          <Alert variant="destructive">
            <AlertCircle className="size-4" aria-hidden />
            <AlertTitle>Device error</AlertTitle>
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
  const [expandedId, setExpandedId] = useState<string | null>(null)

  if (devices === null) {
    return <p className="text-muted-foreground text-sm">Loading devices…</p>
  }
  if (devices.length === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        No devices enrolled yet. Mint an enrolment token above, then run{" "}
        <code className="font-mono text-xs">skillfleet-agent enroll</code> on the target host.
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
                  <span className="ml-2">{describePlatform(d)}</span>
                  <span className="ml-2">
                    · last seen {formatLastSeen(d.last_seen_at, renderedAt)}
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
                  Skills
                </Button>
                <DeviceActions
                  device={d}
                  busy={busyId === d.id}
                  onApprove={() => onApprove(d.id)}
                  onRevoke={() => onRevoke(d.id)}
                />
              </div>
            </div>
            {expanded ? <InventoryMatrix deviceId={d.id} /> : null}
          </li>
        )
      })}
    </ul>
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
  if (device.status === "revoked") return null
  return (
    <div className="flex items-center gap-2">
      {device.status === "pending" && (
        <Button size="sm" onClick={onApprove} disabled={busy}>
          {busy ? "Approving…" : "Approve"}
        </Button>
      )}
      <Button size="sm" variant="ghost" onClick={onRevoke} disabled={busy}>
        {busy ? "Revoking…" : "Revoke"}
      </Button>
    </div>
  )
}

function StatusBadge({ status }: { status: Device["status"] }) {
  const colour =
    status === "approved"
      ? "text-emerald-600"
      : status === "pending"
        ? "text-amber-600"
        : "text-muted-foreground"
  return <span className={`font-medium uppercase tracking-wide ${colour}`}>{status}</span>
}

function describePlatform(d: Device): string {
  const parts = [d.os, d.arch, d.agent_version].filter(Boolean)
  return parts.join(" / ") || "unknown platform"
}

// formatLastSeen returns "never", "5s ago", "2m ago", etc. Captures
// renderedAt at the call site (not inline Date.now()) to keep render
// deterministic for the react-hooks/purity rule.
function formatLastSeen(ts: number | undefined, renderedAt: number): string {
  if (!ts) return "never"
  const delta = Math.max(0, renderedAt - ts)
  if (delta < 5_000) return "just now"
  if (delta < 60_000) return `${Math.floor(delta / 1000)}s ago`
  if (delta < 60 * 60_000) return `${Math.floor(delta / 60_000)}m ago`
  if (delta < 24 * 60 * 60_000) return `${Math.floor(delta / 3_600_000)}h ago`
  return new Date(ts).toLocaleString()
}
