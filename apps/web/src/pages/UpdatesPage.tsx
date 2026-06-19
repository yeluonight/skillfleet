import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useNavigate } from "react-router-dom"

import { UpdatesCard, type UpdateActions } from "@/components/UpdatesCard"
import { UpstreamDiffView } from "@/components/UpstreamDiffView"
import { ThreeWayMergeView, type MergeChoice } from "@/components/ThreeWayMergeView"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { api, apiErrorMessage, updateItemKey } from "@/lib/api"
import type {
  ThreeWayDiff,
  UpdateItem,
  UpdatesResponse,
  UpstreamDiff,
} from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"

// UpdatesPage is the §13.7 six-dimension Updates route. It owns the
// api.listUpdates() fetch + refresh and the per-item action handlers
// (mgmt-refactor track D): view upstream / three-way diff in a dialog,
// re-check a binding, and capture a device's local edit into the registry
// (reusing Track A adoption). Selecting a skill routes to its detail page,
// where the full deploy flow (defaulting to the current version) lives.
export function UpdatesPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { data, loading, error, refresh } = useApiResource<UpdatesResponse>(
    () => api.listUpdates(),
    { errorFallback: t("updates.loadFailed") },
  )

  // Diff dialogs (two-way upstream + three-way). Each owns its own
  // fetch/loading/error, mirroring SkillDetailPanel's pattern.
  const [diffName, setDiffName] = useState<string | null>(null)
  const [diff, setDiff] = useState<UpstreamDiff | null>(null)
  const [diffLoading, setDiffLoading] = useState(false)
  const [diffError, setDiffError] = useState<string | null>(null)

  const [threeWayItem, setThreeWayItem] = useState<UpdateItem | null>(null)
  const [threeWay, setThreeWay] = useState<ThreeWayDiff | null>(null)
  const [threeWayLoading, setThreeWayLoading] = useState(false)
  const [threeWayError, setThreeWayError] = useState<string | null>(null)
  const [mergeChoices, setMergeChoices] = useState<Record<string, MergeChoice>>({})

  // Per-row busy + transient action error (capture/recheck).
  const [busyKey, setBusyKey] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  async function loadDiff(name: string) {
    setDiffLoading(true)
    setDiffError(null)
    try {
      setDiff(await api.upstreamDiff(name))
    } catch (err) {
      setDiffError(apiErrorMessage(err, t("updates.loadFailed")))
    } finally {
      setDiffLoading(false)
    }
  }

  async function loadThreeWay(item: UpdateItem) {
    setThreeWayLoading(true)
    setThreeWayError(null)
    try {
      const local = item.device_id
        ? { deviceId: item.device_id, toolKey: item.tool_key, scope: item.scope }
        : undefined
      setThreeWay(await api.threeWayDiff(item.name, local))
    } catch (err) {
      setThreeWayError(apiErrorMessage(err, t("updates.loadFailed")))
    } finally {
      setThreeWayLoading(false)
    }
  }

  const actions: UpdateActions = {
    busyKey,
    onViewDiff: (item) => {
      setDiffName(item.name)
      setDiff(null)
      void loadDiff(item.name)
    },
    onViewThreeWay: (item) => {
      setThreeWayItem(item)
      setThreeWay(null)
      setMergeChoices({})
      void loadThreeWay(item)
    },
    onRecheck: async (item) => {
      setBusyKey(updateItemKey(item))
      setActionError(null)
      try {
        await api.checkUpdates(item.name)
        await refresh()
      } catch (err) {
        setActionError(apiErrorMessage(err, t("updates.recheckFailed")))
      } finally {
        setBusyKey(null)
      }
    },
    onCapture: async (item) => {
      if (!item.device_id) return
      setBusyKey(updateItemKey(item))
      setActionError(null)
      try {
        await api.adoptDeviceSkill(item.device_id, item.name, {
          tool_key: item.tool_key,
          scope: item.scope,
        })
        await refresh()
      } catch (err) {
        setActionError(apiErrorMessage(err, t("updates.captureFailed")))
      } finally {
        setBusyKey(null)
      }
    },
  }

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-semibold tracking-tight">{t("nav.updates")}</h1>
      {actionError ? <p className="text-state-danger-600 text-sm">{actionError}</p> : null}
      <UpdatesCard
        data={data}
        error={error}
        busy={loading}
        onRefresh={() => void refresh()}
        onSelectSkill={(name) => navigate(`/skills/${encodeURIComponent(name)}`)}
        actions={actions}
      />

      <Dialog open={diffName !== null} onOpenChange={(o) => !o && setDiffName(null)}>
        <DialogContent className="max-h-[90vh] overflow-auto sm:max-w-5xl">
          <DialogHeader>
            <DialogTitle>{t("sources.upstreamDiffTitle", { name: diffName ?? "" })}</DialogTitle>
            <DialogDescription>{t("sources.upstreamDiffDesc")}</DialogDescription>
          </DialogHeader>
          <UpstreamDiffView
            diff={diff}
            loading={diffLoading}
            error={diffError}
            onRefresh={() => diffName && void loadDiff(diffName)}
          />
        </DialogContent>
      </Dialog>

      <Dialog open={threeWayItem !== null} onOpenChange={(o) => !o && setThreeWayItem(null)}>
        <DialogContent className="max-h-[90vh] overflow-auto sm:max-w-5xl">
          <DialogHeader>
            <DialogTitle>{t("sources.threeWayTitle", { name: threeWayItem?.name ?? "" })}</DialogTitle>
            <DialogDescription>{t("sources.threeWayDesc")}</DialogDescription>
          </DialogHeader>
          <ThreeWayMergeView
            diff={threeWay}
            loading={threeWayLoading}
            error={threeWayError}
            choices={mergeChoices}
            onChoose={(path, choice) => setMergeChoices((prev) => ({ ...prev, [path]: choice }))}
            onRefresh={() => threeWayItem && void loadThreeWay(threeWayItem)}
          />
        </DialogContent>
      </Dialog>
    </div>
  )
}
