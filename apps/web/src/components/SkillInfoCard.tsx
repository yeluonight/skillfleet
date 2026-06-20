import { memo } from "react"
import { useTranslation } from "react-i18next"
import type { SkillDetail, FleetStatus } from "@/lib/api"

// SkillInfoCard is the 6-cell summary strip atop the skill detail page
// (aligns to docs/ui-preview.html:848). Pure presentational: it derives
// every cell from the already-fetched detail + fleet status — no own state
// or API calls. Deployment/Drift/Tools come from fleet-status (a skill with
// no deployments shows not-deployed/—); Created approximates the skill's
// first version via the oldest version's created_at.
//
// memo: the card sits above the Tabs and stays mounted across tab switches,
// but its props (detail, fleetStatus) are stable references from
// useApiResource unless the data actually changes — so memo skips the
// re-render on every setActiveTab.
export const SkillInfoCard = memo(function SkillInfoCard({
  detail,
  fleetStatus,
}: {
  detail: SkillDetail
  fleetStatus: FleetStatus | null
}) {
  const { t } = useTranslation()
  const deployments = fleetStatus?.deployments ?? []
  const deviceCount = new Set(deployments.map((d) => d.device_id)).size
  const targetCount = deployments.length
  const driftCount = deployments.filter((d) => d.local_state === "local_modified").length
  const tools = [...new Set(deployments.map((d) => d.tool_key))]
  const latest = detail.versions[0]
  const created = detail.versions.length
    ? Math.min(...detail.versions.map((v) => v.created_at))
    : null

  const deploymentValue =
    targetCount === 0
      ? t("skills.infoCard.notDeployed")
      : t("skills.infoCard.deploymentValue", { devices: deviceCount, targets: targetCount })
  const versionsValue =
    detail.versions.length === 0
      ? "—"
      : `${detail.versions.length} · ${t("skills.infoCard.latest", {
          label: latest?.version_label ?? latest?.id ?? "",
        })}`
  // Reuse the existing sources.state.unbound label rather than a duplicate
  // infoCard-specific key.
  const sourceValue =
    detail.source_state === "bound" ? detail.source?.url ?? "—" : t("sources.state.unbound")

  const cells = [
    { label: t("skills.infoCard.source"), value: sourceValue },
    { label: t("skills.infoCard.versions"), value: versionsValue },
    { label: t("skills.infoCard.deployment"), value: deploymentValue },
    {
      label: t("skills.infoCard.drift"),
      value: t("skills.infoCard.driftValue", { count: driftCount }),
      warn: driftCount > 0,
    },
    { label: t("skills.infoCard.tools"), value: tools.length ? tools.join(", ") : "—" },
    { label: t("skills.infoCard.created"), value: created ? new Date(created).toLocaleDateString() : "—" },
  ]

  return (
    <dl className="bg-border grid grid-cols-2 gap-px overflow-hidden rounded-md border sm:grid-cols-3 lg:grid-cols-6">
      {cells.map((c) => (
        <div key={c.label} className="bg-card space-y-1 p-3">
          <dt className="text-muted-foreground text-xs font-medium">{c.label}</dt>
          <dd
            className={`truncate text-sm font-medium ${c.warn ? "text-state-warn-600" : ""}`}
            title={c.value}
          >
            {c.value}
          </dd>
        </div>
      ))}
    </dl>
  )
})
