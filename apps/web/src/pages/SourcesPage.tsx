import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router-dom"
import { ChevronRight, GitBranch, Link2 } from "lucide-react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api"
import type { SkillSummary } from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"

// SourcesPage. Source binding is a per-skill capability that lives inside the
// Skills registry (SkillDetailPanel's SourceSection), not a standalone fleet
// resource — there is no "sources list" entity server-side. So this route is
// an index + signpost: it explains where binding happens and lists the skills
// that already have a bound upstream, each linking back to the Skills page
// where the operator checks updates / views diffs / detaches.
export function SourcesPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { data } = useApiResource<{ skills: SkillSummary[] }>(
    () => api.listSkills(),
    { errorFallback: t("sources.pageLoadFailed") },
  )
  const bound = data?.skills.filter((s) => s.source_state === "bound") ?? null

  return (
    <div className="mx-auto max-w-4xl space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">{t("nav.sources")}</h1>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-lg">
            <GitBranch className="text-primary size-5" aria-hidden />
            {t("sources.bindingTitle")}
          </CardTitle>
          <CardDescription>{t("sources.pageDesc")}</CardDescription>
        </CardHeader>
        <CardContent>
          <Button size="sm" onClick={() => navigate("/skills")}>
            {t("sources.pageGoSkills")}
            <ChevronRight className="size-4" aria-hidden />
          </Button>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-lg">{t("sources.pageBoundTitle")}</CardTitle>
        </CardHeader>
        <CardContent>
          {bound === null ? (
            <div className="space-y-2">
              <Skeleton className="h-9 w-full" />
              <Skeleton className="h-9 w-full" />
            </div>
          ) : bound.length === 0 ? (
            <p className="text-muted-foreground text-sm">{t("sources.pageBoundEmpty")}</p>
          ) : (
            <ul className="divide-border divide-y rounded-md border">
              {bound.map((s) => (
                <li key={s.name}>
                  <button
                    className="flex w-full items-center justify-between gap-3 px-3 py-2.5 text-left hover:bg-muted/50"
                    onClick={() => navigate("/skills")}
                  >
                    <span className="flex min-w-0 items-center gap-2">
                      <Link2 className="text-state-clean-600 size-4 shrink-0" aria-hidden />
                      <span className="truncate text-sm font-medium">{s.name}</span>
                    </span>
                    <ChevronRight className="text-muted-foreground size-4 shrink-0" aria-hidden />
                  </button>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
