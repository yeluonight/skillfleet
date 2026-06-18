import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import type { Device } from "@/lib/api"

// DeviceActions renders the approve/revoke button pair for a device,
// gated by its lifecycle status (hidden once revoked, approve shown only
// while pending). Shared by the devices list and the device detail page;
// callers own the busy state and the approve/revoke handlers.
export function DeviceActions({
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
