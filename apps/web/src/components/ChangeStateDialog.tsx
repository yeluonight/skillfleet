import { ArrowRight } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useAsyncAction } from "@/hooks/useAsyncAction"
import { api, supportedStatesForTool } from "@/lib/api"
import type { EffectiveState } from "@/lib/api"
import { StateBadge } from "@/lib/status-meta"
import { useState } from "react"

// ChangeStateDialog is the controlled confirm-and-apply dialog behind the
// InventoryMatrix State cell (phase 9). The parent owns `open` and the
// target identity (device + tool + scope + skill + current state); this
// component lets the operator pick a new state from the tool's supported
// set, shows the "X → Y" change as a confirmation step (the danger-op
// "plan" per §24), POSTs the state_change job, and calls onApplied so the
// parent can note it. The new state lands after the device's next scan,
// which the success note makes explicit.
//
// Reset semantics: the parent mounts this with a key tied to the target
// skill (see InventoryMatrix), so switching skills re-mounts the dialog
// and `desired`/`done` initialise fresh — no reset effect needed (which
// also keeps clear of react-hooks/set-state-in-effect).
//
// Every api call + CSRF + error handling lives here (mirrors DeploySection);
// the matrix cell is just a button that opens this.
export function ChangeStateDialog({
  open,
  onOpenChange,
  deviceId,
  toolKey,
  scope,
  skillName,
  currentState,
  onApplied,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  deviceId: string
  toolKey: string
  scope: string
  skillName: string
  currentState: EffectiveState
  onApplied?: () => void
}) {
  const options = supportedStatesForTool(toolKey)
  const { t } = useTranslation()
  const [desired, setDesired] = useState<EffectiveState>(currentState)
  const [done, setDone] = useState(false)
  const action = useAsyncAction()

  async function apply() {
    const ok = await action.run(
      () =>
        api.changeSkillState({
          skill_name: skillName,
          tool_key: toolKey,
          scope,
          device_id: deviceId,
          desired_state: desired,
        }),
      t("deploys.err.changeState"),
    )
    if (ok) {
      setDone(true)
      onApplied?.()
    }
  }

  const unchanged = desired === currentState

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("deploys.changeStateTitle")}</DialogTitle>
          <DialogDescription>
            {skillName} · {toolKey} · {scope}
          </DialogDescription>
        </DialogHeader>

        {done ? (
          <div className="space-y-2 text-sm">
            <p>
              {t("deploys.appliedPrefix")} <span className="font-medium">{skillName}</span>{" "}
              {t("deploys.appliedSuffix")} <StateBadge state={desired} />。
            </p>
            <p className="text-muted-foreground text-xs">
              {t("deploys.appliedScanHint")}
            </p>
          </div>
        ) : (
          <div className="space-y-3">
            <label className="block text-sm">
              <span className="text-muted-foreground mb-1 block text-xs">{t("deploys.targetState")}</span>
              <select
                className="bg-background h-9 w-full rounded-md border px-2 text-sm"
                value={desired}
                onChange={(e) => setDesired(e.target.value as EffectiveState)}
              >
                {options.map((s) => (
                  <option key={s} value={s}>
                    {s}
                  </option>
                ))}
              </select>
            </label>

            {/* Confirmation step: show the current → desired transition so
                the operator sees exactly what will change before applying. */}
            <div className="text-muted-foreground flex items-center gap-2 text-sm">
              <StateBadge state={currentState} />
              <ArrowRight className="size-4" aria-hidden />
              <StateBadge state={desired} />
            </div>

            <p className="text-muted-foreground text-xs">{t("deploys.auditNote")}</p>

            {action.error ? <p className="text-state-danger-600 text-sm">{action.error}</p> : null}
          </div>
        )}

        <DialogFooter>
          {done ? (
            <Button onClick={() => onOpenChange(false)}>{t("deploys.confirmDone")}</Button>
          ) : (
            <>
              <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={action.busy}>
                {t("common.cancel")}
              </Button>
              <Button onClick={apply} disabled={action.busy || unchanged}>
                {unchanged ? t("deploys.stateUnchanged") : t("deploys.confirmApply")}
              </Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
