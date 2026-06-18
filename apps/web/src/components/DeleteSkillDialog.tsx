import { useTranslation } from "react-i18next"
import { useAsyncAction } from "@/hooks/useAsyncAction"
import { api } from "@/lib/api"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

// DeleteSkillDialog removes a skill from the registry via DELETE
// /api/skills/{name}. The on-disk device files are NOT uninstalled — the
// description makes that explicit so the operator isn't surprised.
export function DeleteSkillDialog({
  open,
  onOpenChange,
  name,
  onDeleted,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  name: string
  onDeleted: () => void
}) {
  const { t } = useTranslation()
  const action = useAsyncAction()

  async function submit() {
    const ok = await action.run(() => api.deleteSkill(name), t("skills.err.deleteSkill"))
    if (ok) {
      onOpenChange(false)
      onDeleted()
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="text-state-danger-600">
            {t("skills.deleteSkillTitle")}
          </DialogTitle>
          <DialogDescription>{t("skills.deleteSkillDesc", { name })}</DialogDescription>
        </DialogHeader>
        {action.error && <p className="text-state-danger-600 text-sm">{action.error}</p>}
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button variant="destructive" onClick={submit} disabled={action.busy}>
            {t("skills.deleteSkillConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
