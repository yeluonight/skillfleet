import { useState } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ArrowLeft, Pencil, Trash2 } from "lucide-react"
import { SkillDetailPanel } from "@/components/SkillDetailPanel"
import { SkillFleetMatrix } from "@/components/SkillFleetMatrix"
import { RenameSkillDialog } from "@/components/RenameSkillDialog"
import { DeleteSkillDialog } from "@/components/DeleteSkillDialog"

// SkillDetailPage is the per-skill workspace (优化改造 §5.4 Step1+2).
// Replaces the inline accordion in SkillsCard: the list is an index, this is
// the work area. Two tabs — 部署全景 (Track 05 fills with SkillFleetMatrix)
// and 详情, which reuses SkillDetailPanel as-is (it already composes
// versions/source/deploy with draft switching + key remount), so the panel's
// stateful internals stay encapsulated (§5.7 #1).
//
// Note: useParams is used here for the first time in the codebase.
export function SkillDetailPage() {
  const { t } = useTranslation()
  const { name = "" } = useParams<{ name: string }>()
  const navigate = useNavigate()
  const [renameOpen, setRenameOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" onClick={() => navigate("/skills")}>
          <ArrowLeft className="size-4" aria-hidden />
          {t("skills.backToSkills")}
        </Button>
        <h1 className="text-xl font-semibold">{name}</h1>
        <div className="ml-auto flex items-center gap-2">
          <Button variant="ghost" size="sm" onClick={() => setRenameOpen(true)}>
            <Pencil className="size-4" aria-hidden />
            {t("skills.renameSkillConfirm")}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setDeleteOpen(true)}>
            <Trash2 className="size-4" aria-hidden />
            {t("skills.deleteSkillConfirm")}
          </Button>
        </div>
      </div>

      <Tabs defaultValue="fleet" className="w-full">
        <TabsList>
          <TabsTrigger value="fleet">{t("skills.tabs.fleet")}</TabsTrigger>
          <TabsTrigger value="detail">{t("skills.tabs.detail")}</TabsTrigger>
        </TabsList>

        {/* Deploy overview: cross-device deployment state (Track 05). */}
        <TabsContent value="fleet">
          <SkillFleetMatrix skillName={name} />
        </TabsContent>

        <TabsContent value="detail">
          {/* The list lives on another route now, so there is nothing to
              refresh on change — onChanged is a no-op. */}
          <SkillDetailPanel name={name} onChanged={() => {}} />
        </TabsContent>
      </Tabs>

      <RenameSkillDialog
        open={renameOpen}
        onOpenChange={setRenameOpen}
        name={name}
        onRenamed={(newName) => navigate("/skills/" + encodeURIComponent(newName))}
      />
      <DeleteSkillDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        name={name}
        onDeleted={() => navigate("/skills")}
      />
    </div>
  )
}
