import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"
import { type FleetStatus, type FleetDeployment } from "@/lib/api"
import { StateBadge, LocalStateBadge } from "@/lib/status-meta"
import { Button } from "@/components/ui/button"
import { ChangeStateDialog } from "@/components/ChangeStateDialog"

// SkillFleetMatrix renders a skill's deployment footprint across devices
// (优化改造 §3.3 + §5.4 Step7). Each row = one device×tool×scope install,
// with effective_state + computed local_state badges, an active-job
// spinner, and an enable/disable action via ChangeStateDialog. Rollback and
// capture-drift are heavier flows left as follow-ups.
//
// Controlled view: the page owns the fleet-status fetch (shared with the
// skill Info Card) and passes the result down, so switching tabs back to the
// fleet view does not re-request and stays in sync with the summary.
export function SkillFleetMatrix({
  skillName,
  fleetStatus,
  fleetError,
  onRefresh,
}: {
  skillName: string
  fleetStatus: FleetStatus | null
  fleetError: string | null
  onRefresh: () => void
}) {
  const { t } = useTranslation()
  const [changeTarget, setChangeTarget] = useState<FleetDeployment | null>(null)

  if (fleetError) {
    return <p className="text-state-danger-600 text-sm">{fleetError}</p>
  }
  if (!fleetStatus) {
    return <p className="text-muted-foreground text-sm">{t("skills.loadingFleet")}</p>
  }
  if (fleetStatus.deployments.length === 0) {
    return (
      <p className="text-muted-foreground py-8 text-center text-sm">
        {t("skills.fleetEmpty")}
      </p>
    )
  }

  return (
    <div className="space-y-3">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-muted-foreground border-b text-left text-xs">
            <th className="py-2 pr-3 font-medium">{t("skills.fleetDevice")}</th>
            <th className="py-2 pr-3 font-medium">{t("skills.fleetTool")}</th>
            <th className="py-2 pr-3 font-medium">{t("skills.fleetScope")}</th>
            <th className="py-2 pr-3 font-medium">{t("skills.fleetState")}</th>
            <th className="py-2 pr-3 font-medium">{t("skills.fleetDrift")}</th>
            <th className="py-2 pr-3 font-medium">{t("skills.fleetActions")}</th>
          </tr>
        </thead>
        <tbody>
          {fleetStatus.deployments.map((d) => (
            <tr key={`${d.device_id}/${d.tool_key}/${d.scope}`} className="border-b last:border-0">
              <td className="py-2 pr-3">
                <div className="font-medium">{d.device_name}</div>
                <div className="text-muted-foreground font-mono text-[10px]">{d.root_path}</div>
              </td>
              <td className="text-muted-foreground py-2 pr-3">{d.tool_key}</td>
              <td className="text-muted-foreground py-2 pr-3">{d.scope}</td>
              <td className="py-2 pr-3">
                <StateBadge state={d.effective_state} />
              </td>
              <td className="py-2 pr-3">
                <LocalStateBadge state={d.local_state} />
              </td>
              <td className="py-2 pr-3">
                <div className="flex items-center gap-1">
                  {d.has_active_job ? (
                    <Loader2 className="size-4 animate-spin" aria-label={t("skills.fleetActiveJob")} />
                  ) : null}
                  <Button variant="ghost" size="sm" onClick={() => setChangeTarget(d)}>
                    {d.effective_state === "on" ? t("skills.disable") : t("skills.enable")}
                  </Button>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {changeTarget && (
        <ChangeStateDialog
          open={true}
          onOpenChange={(v) => !v && setChangeTarget(null)}
          deviceId={changeTarget.device_id}
          toolKey={changeTarget.tool_key}
          scope={changeTarget.scope}
          skillName={skillName}
          currentState={changeTarget.effective_state}
          onApplied={onRefresh}
        />
      )}
    </div>
  )
}
