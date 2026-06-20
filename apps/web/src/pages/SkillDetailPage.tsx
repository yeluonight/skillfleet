import { useState } from "react"
import type { ReactNode } from "react"
import { useNavigate, useParams } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { ArrowLeft, GitBranch, Pencil, Trash2 } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { SourceSection, VersionList, DraftEditor } from "@/components/SkillDetailPanel"
import { SkillFleetMatrix } from "@/components/SkillFleetMatrix"
import { SkillInfoCard } from "@/components/SkillInfoCard"
import { DeploySection } from "@/components/DeploySection"
import { RenameSkillDialog } from "@/components/RenameSkillDialog"
import { DeleteSkillDialog } from "@/components/DeleteSkillDialog"
import { useSkillDetail } from "@/hooks/useSkillDetail"
import { useApiResource } from "@/hooks/useApiResource"
import { api, type FleetStatus, type SkillDetail } from "@/lib/api"

// SkillDetailPage is the per-skill workspace (优化改造 §5.4, aligned to
// docs/ui-preview.html:832). Four flat tabs — 部署全景 / 源绑定 / 版本 / 编辑 —
// sit under a 6-cell SkillInfoCard aggregating detail + fleet status.
//
// The detail + draft state is lifted here via useSkillDetail and shared
// across the 源绑定/版本/编辑 tabs; fleet status is fetched once and shared by
// the Info Card and the fleet matrix. The active tab is controlled so fork /
// publish / discard can drive tab switches (fork -> 编辑, publish/discard ->
// 版本). Radix Tabs unmount inactive content (no forceMount): the editor's
// unsaved buffer is lost on tab switch but the draft itself is preserved —
// an accepted trade-off (better than the old 2-tab layout, which lost the
// draft reference on a fleet-tab switch). (Track 11.)
//
// Note: useParams is used here for the first time in the codebase.
export function SkillDetailPage() {
  const { t } = useTranslation()
  const { name = "" } = useParams<{ name: string }>()
  const navigate = useNavigate()
  const [renameOpen, setRenameOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [activeTab, setActiveTab] = useState("fleet")

  // The list lives on another route now, so there is nothing to refresh on
  // change — onChanged is a no-op.
  const sd = useSkillDetail(name, () => {})
  const {
    data: fleetStatus,
    error: fleetError,
    refresh: refreshFleet,
  } = useApiResource<FleetStatus>(() => api.skillFleetStatus(name), {
    deps: [name],
    errorFallback: t("skills.err.loadFleet"),
  })

  // fork: start a draft from the latest version, then jump to the editor tab.
  // startDraft returns whether it succeeded; reading sd.draft right after the
  // await would hit a stale closure (state hasn't re-rendered yet).
  async function handleFork() {
    if (await sd.startDraft()) setActiveTab("editor")
  }
  // publish/discard clear the draft and return to the versions tab.
  function handlePublished() {
    sd.clearDraft()
    void sd.refresh()
    setActiveTab("versions")
  }
  function handleDiscarded() {
    sd.clearDraft()
    setActiveTab("versions")
  }

  // Fork button shared by the 版本 and 编辑(empty) tabs.
  const forkButton = (
    <Button
      size="sm"
      variant="secondary"
      onClick={handleFork}
      disabled={!sd.detail || sd.detail.versions.length === 0}
    >
      <GitBranch className="size-4" aria-hidden />
      {t("skills.editFork")}
    </Button>
  )

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

      {sd.detail && <SkillInfoCard detail={sd.detail} fleetStatus={fleetStatus} />}

      <Tabs value={activeTab} onValueChange={setActiveTab} className="w-full">
        <TabsList>
          <TabsTrigger value="fleet">{t("skills.tabs.fleet")}</TabsTrigger>
          <TabsTrigger value="source">{t("skills.tabs.source")}</TabsTrigger>
          <TabsTrigger value="versions">{t("skills.tabs.versions")}</TabsTrigger>
          <TabsTrigger value="editor">{t("skills.tabs.editor")}</TabsTrigger>
        </TabsList>

        {/* 部署全景: cross-device matrix + deploy jobs (version picker +
            job list with rollback). DeploySection keyed by name so it resets
            on skill switch; its 4s job-list poll runs only while this tab is
            active (Radix unmount stops it). */}
        <TabsContent value="fleet">
          <div className="mt-3 space-y-4">
            <SkillFleetMatrix
              key={name}
              skillName={name}
              fleetStatus={fleetStatus}
              fleetError={fleetError}
              onRefresh={refreshFleet}
              versions={sd.detail?.versions}
              currentVersionId={sd.detail?.current_version_id}
              onDeployed={refreshFleet}
            />
            {sd.detail && (
              <DeploySection
                key={`deploy-${name}`}
                skillName={name}
                versions={sd.detail.versions}
                currentVersionId={sd.detail.current_version_id}
              />
            )}
          </div>
        </TabsContent>

        {/* 源绑定: key={name} remounts on skill switch, resetting the
            bind/preview/check/merge wizard state without an effect. All
            source api + CSRF lives in SourceSection. */}
        <TabsContent value="source">
          <DetailGuard error={sd.error} detail={sd.detail}>
            {(d) => (
              <SourceSection
                key={name}
                name={name}
                sourceState={d.source_state ?? "unbound"}
                source={d.source}
                lastCheckedAt={d.last_checked_at}
                onChanged={sd.onSourceChanged}
              />
            )}
          </DetailGuard>
        </TabsContent>

        {/* 版本: version list only — no rollback button here (rollback lives
            in 部署全景's job list). Fork button starts a draft and jumps to
            编辑. */}
        <TabsContent value="versions">
          <DetailGuard error={sd.error} detail={sd.detail}>
            {(d) => (
              <div className="mt-3 space-y-4">
                <VersionList
                  versions={d.versions}
                  name={name}
                  currentVersionId={d.current_version_id}
                  onChanged={() => void sd.refresh()}
                />
                {forkButton}
              </div>
            )}
          </DetailGuard>
        </TabsContent>

        {/* 编辑: 3-pane draft editor, or a fork entry when there is no draft.
            key={draft.id} guards against a future resume-draft reusing stale
            editor state. Radix unmounts this tab when inactive (no
            forceMount) — the unsaved editor buffer is lost on tab switch, but
            the draft itself is preserved (accepted trade-off). */}
        <TabsContent value="editor">
          <DetailGuard error={sd.error} detail={sd.detail}>
            {() =>
              sd.draft ? (
                <DraftEditor
                  key={sd.draft.id}
                  draft={sd.draft}
                  onPublished={handlePublished}
                  onDiscarded={handleDiscarded}
                />
              ) : (
                <div className="mt-3 space-y-3">
                  <p className="text-muted-foreground text-sm">{t("skills.editorEmpty")}</p>
                  {forkButton}
                </div>
              )
            }
          </DetailGuard>
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

// DetailGuard renders the shared skill-detail loading/error state, then the
// tab body once the detail is loaded. The body is a render prop so each tab
// gets a narrowed (non-null) SkillDetail instead of re-checking null inline
// (the source/versions/editor tabs all shared this guard verbatim).
function DetailGuard({
  error,
  detail,
  children,
}: {
  error: string | null
  detail: SkillDetail | null
  children: (detail: SkillDetail) => ReactNode
}) {
  const { t } = useTranslation()
  if (error) {
    return <p className="text-state-danger-600 mt-3 text-xs">{error}</p>
  }
  if (detail === null) {
    return <p className="text-muted-foreground mt-3 text-sm">{t("skills.loadingDetail")}</p>
  }
  return <>{children(detail)}</>
}
