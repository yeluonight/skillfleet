import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router-dom"

import { UpdatesCard } from "@/components/UpdatesCard"
import { api } from "@/lib/api"
import type { UpdatesResponse } from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"

// UpdatesPage is the §13.7 six-dimension Updates route. It owns the
// api.listUpdates() fetch + refresh and hands UpdatesCard a controlled view.
// Selecting a skill routes to the Skills registry, where source binding,
// upstream-diff, and three-way-merge actions live (a per-skill capability —
// there is no fleet-wide update action surface).
export function UpdatesPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { data, loading, error, refresh } = useApiResource<UpdatesResponse>(
    () => api.listUpdates(),
    { errorFallback: t("updates.loadFailed") },
  )

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">{t("nav.updates")}</h1>
      <UpdatesCard
        data={data}
        error={error}
        busy={loading}
        onRefresh={() => void refresh()}
        onSelectSkill={() => navigate("/skills")}
      />
    </div>
  )
}
