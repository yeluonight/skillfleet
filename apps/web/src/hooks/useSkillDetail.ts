import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useApiResource } from "@/hooks/useApiResource"
import { api, apiErrorMessage } from "@/lib/api"
import type { Draft, SkillDetail } from "@/lib/api"

// useSkillDetail owns one skill's detail resource plus the draft lifecycle
// (fork/publish/discard). Extracted from SkillDetailPanel so the detail +
// draft state can be shared across the skill-detail tabs (Track 11), not
// just a single panel. The hook is tab-agnostic; the page wires fork /
// publish / discard to tab switches.
export function useSkillDetail(name: string, onChanged: () => void) {
  const { t } = useTranslation()
  const {
    data: detail,
    error,
    refresh: loadDetail,
    setError,
  } = useApiResource<SkillDetail>(() => api.getSkill(name), {
    deps: [name],
    errorFallback: t("skills.err.loadVersions"),
  })
  const [draft, setDraft] = useState<Draft | null>(null)

  // startDraft forks the latest version into a new draft. Returns whether a
  // draft was created, so the caller can react (e.g. switch to the editor
  // tab) only on success — reading `draft` from the hook right after the
  // await would hit a stale closure (the state update has not re-rendered
  // yet, so the captured `draft` is still null).
  async function startDraft(): Promise<boolean> {
    const versions = detail?.versions
    if (!versions || versions.length === 0) return false
    try {
      const d = await api.createDraft({ base_version_id: versions[0].id })
      setDraft(d)
      setError(null)
      return true
    } catch (err) {
      setError(apiErrorMessage(err, t("skills.err.createDraft")))
      return false
    }
  }

  // onSourceChanged refreshes the detail (so the SourceTab reflects the new
  // bound state / last_checked_at) and bubbles up to the list for its badge.
  async function onSourceChanged() {
    await loadDetail()
    onChanged()
  }

  return {
    detail,
    error,
    refresh: loadDetail,
    draft,
    startDraft,
    clearDraft: () => setDraft(null),
    onSourceChanged,
  }
}
