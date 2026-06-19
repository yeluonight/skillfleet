import { useState } from "react"
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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

// RenameSkillDialog renames a skill via POST /api/skills/{name}/rename.
// Empty or unchanged names close without a request (the backend is
// idempotent, but we avoid the round-trip).
export function RenameSkillDialog({
  open,
  onOpenChange,
  name,
  onRenamed,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  name: string
  onRenamed: (newName: string) => void
}) {
  const { t } = useTranslation()
  const [newName, setNewName] = useState(name)
  const action = useAsyncAction()

  async function submit() {
    const trimmed = newName.trim()
    if (!trimmed || trimmed === name) {
      onOpenChange(false)
      return
    }
    const ok = await action.run(() => api.renameSkill(name, trimmed), t("skills.err.renameSkill"))
    if (ok) {
      onOpenChange(false)
      onRenamed(trimmed)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("skills.renameSkillTitle")}</DialogTitle>
          <DialogDescription>{t("skills.renameSkillDesc", { name })}</DialogDescription>
        </DialogHeader>
        <div className="space-y-2">
          <Label htmlFor="rename-new">{t("skills.renameSkillLabel")}</Label>
          <Input
            id="rename-new"
            value={newName}
            autoFocus
            onChange={(e) => setNewName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void submit()
            }}
          />
        </div>
        {action.error && <p className="text-state-danger-600 text-sm">{action.error}</p>}
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} disabled={action.busy}>
            {t("skills.renameSkillConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
