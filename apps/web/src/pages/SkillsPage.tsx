import { useTranslation } from "react-i18next"

import { SkillsCard } from "@/components/SkillsCard"

// SkillsPage is the §13.8 Skills route: the registry surface. SkillsCard owns
// the list + create/import affordances and expands each row into SkillDetailPanel
// (versions, source binding, draft editor, deploy section).
export function SkillsPage() {
  const { t } = useTranslation()
  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">{t("nav.skills")}</h1>
      <SkillsCard />
    </div>
  )
}
