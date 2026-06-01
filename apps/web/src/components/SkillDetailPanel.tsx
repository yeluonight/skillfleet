import { useEffect, useRef, useState } from "react"
import { Braces, FileCode, FilePlus, GitBranch, GitMerge, Minimize2, Save, Send, Trash2, Upload } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuSeparator, ContextMenuTrigger } from "@/components/ui/context-menu"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { MonacoFileEditor } from "@/components/MonacoFileEditor"
import { SplitPane } from "@/components/SplitPane"
import { ValidationPanel } from "@/components/ValidationPanel"
import { MarkdownPreview } from "@/components/MarkdownPreview"
import { BinaryFileView } from "@/components/BinaryFileView"
import { CommandPalette } from "@/components/CommandPalette"
import type { CommandAction } from "@/components/CommandPalette"
import { SourceTab } from "@/components/SourceTab"
import { BindWizard } from "@/components/BindWizard"
import { UpstreamDiffView } from "@/components/UpstreamDiffView"
import { ThreeWayMergeView } from "@/components/ThreeWayMergeView"
import type { MergeChoice } from "@/components/ThreeWayMergeView"
import { DeploySection } from "@/components/DeploySection"

import { api, apiErrorMessage } from "@/lib/api"
import { formatJson, minifyJson } from "@/lib/json-tools"
import { useApiResource } from "@/hooks/useApiResource"
import type {
  BindPreview,
  BindSourceParams,
  Draft,
  SkillDetail,
  SkillVersion,
  SourceState,
  SourceView,
  ThreeWayDiff,
  UpstreamDiff,
  UpstreamState,
  ValidationIssue,
  VersionFileEntry,
} from "@/lib/api"
import type { editor } from "monaco-editor"

// SkillDetailPanel renders one skill's source binding, version history, the
// latest version's file tree, and a draft editor that forks the latest
// version, edits files, and publishes a new version.
//
// Phase 6 adds the source section at the top: a SourceTab (bound-state +
// check/detach) and a BindWizard. All network + CSRF + error handling for
// those lives HERE (the SourceTab/BindWizard components are pure/controlled);
// the helpers below own the api calls and surface ApiError messages inline.
export function SkillDetailPanel({ name, onChanged }: { name: string; onChanged: () => void }) {
  const {
    data: detail,
    error,
    refresh: loadDetail,
    setError,
  } = useApiResource<SkillDetail>(() => api.getSkill(name), {
    deps: [name],
    errorFallback: "Failed to load versions.",
  })
  const [draft, setDraft] = useState<Draft | null>(null)

  async function startDraft() {
    const versions = detail?.versions
    if (!versions || versions.length === 0) return
    try {
      const d = await api.createDraft({ base_version_id: versions[0].id })
      setDraft(d)
      setError(null)
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to create draft."))
    }
  }

  async function onPublished() {
    setDraft(null)
    await loadDetail()
    onChanged()
  }

  // onSourceChanged refreshes the detail (so the SourceTab reflects the new
  // bound state / last_checked_at) and bubbles up to the list for its badge.
  async function onSourceChanged() {
    await loadDetail()
    onChanged()
  }

  if (error) {
    return <p className="mt-3 text-xs text-red-600">{error}</p>
  }
  if (detail === null) {
    return <p className="text-muted-foreground mt-3 text-sm">Loading…</p>
  }

  const versions = detail.versions

  return (
    <div className="mt-3 space-y-4">
      {/* key={name} remounts the source section on skill switch, resetting its
          wizard/preview/check UI state without an effect (React's reset-on-key
          pattern). All source api + CSRF + error handling lives in it. */}
      <SourceSection
        key={name}
        name={name}
        sourceState={detail.source_state ?? "unbound"}
        source={detail.source}
        lastCheckedAt={detail.last_checked_at}
        onChanged={onSourceChanged}
      />

      {draft ? (
        <DraftEditor draft={draft} onPublished={onPublished} onDiscarded={() => setDraft(null)} />
      ) : (
        <>
          <VersionList versions={versions} />
          <Button size="sm" variant="secondary" onClick={startDraft} disabled={versions.length === 0}>
            <GitBranch className="size-4" aria-hidden />
            Edit (fork latest into a draft)
          </Button>
        </>
      )}

      {/* Deploy section: pick a version + target device and enqueue an
          install job, and see this skill's deployment jobs. Keyed by name
          so it resets on skill switch. All deployment api/CSRF lives here. */}
      <DeploySection key={`deploy-${name}`} skillName={name} versions={versions} />
    </div>
  )
}

// SourceSection owns all source-binding network + CSRF + error handling for
// one skill (phase 6). It is keyed by skill name at the call site, so React
// remounts it on selection change and its wizard/preview/check state resets
// naturally — no prop-derived effects. The SourceTab/BindWizard it renders
// are pure/controlled; this is the single place api.* is called for binding.
function SourceSection({
  name,
  sourceState,
  source,
  lastCheckedAt,
  onChanged,
}: {
  name: string
  sourceState: SourceState
  source?: SourceView
  lastCheckedAt?: number
  onChanged: () => Promise<void> | void
}) {
  const [wizardOpen, setWizardOpen] = useState(false)
  const [preview, setPreview] = useState<BindPreview | null>(null)
  const [previewError, setPreviewError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState<string | null>(null)
  const [lastCheck, setLastCheck] = useState<{
    upstreamState: UpstreamState
    pendingVersionId?: string
    error?: string
  } | null>(null)
  // Upstream diff (t10) modal state.
  const [diffOpen, setDiffOpen] = useState(false)
  const [diff, setDiff] = useState<UpstreamDiff | null>(null)
  const [diffLoading, setDiffLoading] = useState(false)
  const [diffError, setDiffError] = useState<string | null>(null)
  // Three-way merge (phase 7 t7) modal state. base|local|remote — here we
  // open it without a device, so the local side is absent and the view
  // shows base↔remote plus the §5.5 per-file pick. Per-file merge choices
  // are held here (controlled); Phase 7 records intent only — no write-back.
  const [mergeOpen, setMergeOpen] = useState(false)
  const [threeWay, setThreeWay] = useState<ThreeWayDiff | null>(null)
  const [mergeLoading, setMergeLoading] = useState(false)
  const [mergeError, setMergeError] = useState<string | null>(null)
  const [mergeChoices, setMergeChoices] = useState<Record<string, MergeChoice>>({})

  // loadDiff fetches the two-way upstream diff. Shared by the "view diff"
  // affordance and the modal's refresh button.
  async function loadDiff() {
    setDiffLoading(true)
    setDiffError(null)
    try {
      const d = await api.upstreamDiff(name)
      setDiff(d)
    } catch (err) {
      setDiffError(apiErrorMessage(err, "加载差异失败。"))
    } finally {
      setDiffLoading(false)
    }
  }

  function openDiff() {
    setDiffOpen(true)
    void loadDiff()
  }

  // loadThreeWay fetches the base|local|remote diff. Opened without a device
  // here, so the local side is absent (base↔remote + per-file pick only).
  async function loadThreeWay() {
    setMergeLoading(true)
    setMergeError(null)
    try {
      const d = await api.threeWayDiff(name)
      setThreeWay(d)
    } catch (err) {
      setMergeError(apiErrorMessage(err, "加载三方差异失败。"))
    } finally {
      setMergeLoading(false)
    }
  }

  function openMerge() {
    setMergeChoices({})
    setMergeOpen(true)
    void loadThreeWay()
  }

  // requestPreview is the wizard's dry-run probe: it fetches what WOULD be
  // bound without persisting. A fetch error becomes previewError so the
  // wizard keeps the user on the form step to fix the URL/ref/subdir.
  async function requestPreview(params: BindSourceParams) {
    setBusy(true)
    setPreviewError(null)
    try {
      const p = await api.previewBindSource(name, params)
      setPreview(p)
    } catch (err) {
      setPreview(null)
      setPreviewError(apiErrorMessage(err, "拉取预览失败。"))
    } finally {
      setBusy(false)
    }
  }

  // confirmBind performs the real binding. On success it closes the wizard,
  // clears the preview, and asks the parent to reload so the tab reflects the
  // new bound state. On failure it drops the preview (returning the wizard to
  // the form step) so the error is visible there — the form step is the only
  // place previewError renders.
  async function confirmBind(params: BindSourceParams) {
    setBusy(true)
    try {
      await api.bindSource(name, params)
      setWizardOpen(false)
      setPreview(null)
      setPreviewError(null)
      setLastCheck(null)
      await onChanged()
    } catch (err) {
      // Bind errors (already-bound, fetch failure) surface as previewError on
      // the form step; clear the preview so the wizard navigates back to it.
      setPreview(null)
      setPreviewError(apiErrorMessage(err, "绑定失败。"))
    } finally {
      setBusy(false)
    }
  }

  async function checkUpdates() {
    setBusy(true)
    setActionError(null)
    try {
      const res = await api.checkUpdates(name)
      setLastCheck({
        upstreamState: res.upstream_state,
        pendingVersionId: res.pending_version_id,
        error: res.error,
      })
      await onChanged() // last_checked_at advanced server-side
    } catch (err) {
      setActionError(apiErrorMessage(err, "检查更新失败。"))
    } finally {
      setBusy(false)
    }
  }

  async function detachSource() {
    setBusy(true)
    setActionError(null)
    try {
      await api.detachSource(name)
      setLastCheck(null)
      await onChanged()
    } catch (err) {
      setActionError(apiErrorMessage(err, "解绑失败。"))
    } finally {
      setBusy(false)
    }
  }

  function openWizard() {
    setPreview(null)
    setPreviewError(null)
    setWizardOpen(true)
  }

  return (
    <>
      <SourceTab
        sourceState={sourceState}
        source={source}
        lastCheckedAt={lastCheckedAt}
        lastCheck={lastCheck}
        busy={busy}
        onBind={openWizard}
        onCheckUpdates={checkUpdates}
        onDetach={detachSource}
        onViewDiff={openDiff}
      />
      {actionError && <p className="text-xs text-red-600">{actionError}</p>}

      {/* Three-way merge entry (phase 7): only meaningful once bound. The
          view itself handles "no pending update" gracefully, so we surface
          it whenever a binding exists. */}
      {sourceState === "bound" ? (
        <Button type="button" size="sm" variant="ghost" onClick={openMerge} disabled={busy}>
          <GitMerge className="size-4" aria-hidden />
          三方合并
        </Button>
      ) : null}

      <BindWizard
        open={wizardOpen}
        onOpenChange={(open) => {
          setWizardOpen(open)
          if (!open) {
            setPreview(null)
            setPreviewError(null)
          }
        }}
        onPreview={requestPreview}
        onConfirm={confirmBind}
        onBack={() => {
          setPreview(null)
          setPreviewError(null)
        }}
        preview={preview}
        previewError={previewError}
        busy={busy}
      />

      <Dialog open={diffOpen} onOpenChange={setDiffOpen}>
        <DialogContent className="max-h-[90vh] overflow-auto sm:max-w-5xl">
          <DialogHeader>
            <DialogTitle>上游差异 · {name}</DialogTitle>
            <DialogDescription>
              对比当前上游基线与待处理的上游更新（两方差异）。
            </DialogDescription>
          </DialogHeader>
          <UpstreamDiffView
            diff={diff}
            loading={diffLoading}
            error={diffError}
            onRefresh={loadDiff}
          />
        </DialogContent>
      </Dialog>

      <Dialog open={mergeOpen} onOpenChange={setMergeOpen}>
        <DialogContent className="max-h-[90vh] overflow-auto sm:max-w-5xl">
          <DialogHeader>
            <DialogTitle>三方合并 · {name}</DialogTitle>
            <DialogDescription>
              base · 本地 · 上游 三方对比（§5.5）。base↔上游为行级差异；本地副本仅有内容指纹，逐文件本地差异与写回留待 Phase 8。
            </DialogDescription>
          </DialogHeader>
          <ThreeWayMergeView
            diff={threeWay}
            loading={mergeLoading}
            error={mergeError}
            choices={mergeChoices}
            onChoose={(path, choice) =>
              setMergeChoices((prev) => ({ ...prev, [path]: choice }))
            }
            onRefresh={loadThreeWay}
          />
        </DialogContent>
      </Dialog>
    </>
  )
}

function VersionList({ versions }: { versions: SkillVersion[] }) {
  const [filesFor, setFilesFor] = useState<string | null>(versions[0]?.id ?? null)
  return (
    <div className="space-y-2">
      <div className="text-muted-foreground text-xs font-semibold uppercase tracking-wide">
        Versions
      </div>
      <ul className="space-y-1">
        {versions.map((v) => (
          <li key={v.id} className="text-sm">
            <button
              className="flex w-full items-center justify-between gap-2 rounded px-2 py-1 hover:bg-muted/50"
              onClick={() => setFilesFor((cur) => (cur === v.id ? null : v.id))}
            >
              <span className="font-mono text-xs">{v.id}</span>
              <span className="text-muted-foreground text-xs">
                {v.kind}
                {v.version_label ? ` · ${v.version_label}` : ""} · {v.file_count} file
                {v.file_count === 1 ? "" : "s"}
              </span>
            </button>
            {filesFor === v.id ? <VersionFileTree versionId={v.id} /> : null}
          </li>
        ))}
      </ul>
    </div>
  )
}

function VersionFileTree({ versionId }: { versionId: string }) {
  const {
    data,
    error: err,
  } = useApiResource<{ files: VersionFileEntry[] }>(() => api.versionFiles(versionId), {
    deps: [versionId],
    errorFallback: "Failed to load files.",
  })
  const files = data?.files ?? null

  if (err) return <p className="px-2 py-1 text-xs text-red-600">{err}</p>
  if (files === null) return <p className="text-muted-foreground px-2 py-1 text-xs">Loading files…</p>
  return (
    <ul className="border-border ml-4 mt-1 space-y-0.5 border-l pl-3">
      {files.map((f) => (
        <li key={f.path} className="flex items-center justify-between gap-2 text-xs">
          <span className="font-mono">{f.path}</span>
          <span className="text-muted-foreground">
            {f.binary ? "binary" : "text"} · {f.size}B{f.exec ? " · exec" : ""}
            {!f.editable && !f.binary ? " · large" : ""}
          </span>
        </li>
      ))}
    </ul>
  )
}

function DraftEditor({
  draft,
  onPublished,
  onDiscarded,
}: {
  draft: Draft
  onPublished: () => void
  onDiscarded: () => void
}) {
  const [files, setFiles] = useState<Draft["files"]>(draft.files)
  const [selected, setSelected] = useState<string>(
    draft.files.find((f) => f.path === "SKILL.md")?.path ?? draft.files[0]?.path ?? "",
  )
  const [content, setContent] = useState<string>(
    draft.files.find((f) => f.path === selected)?.content ?? "",
  )
  const [dirty, setDirty] = useState(false)
  const [issues, setIssues] = useState<ValidationIssue[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [newPath, setNewPath] = useState("")
  // Dialog state: which file a rename/delete confirm targets.
  const [renaming, setRenaming] = useState<string | null>(null)
  const [renameTo, setRenameTo] = useState("")
  const [deleting, setDeleting] = useState<string | null>(null)
  const uploadRef = useRef<HTMLInputElement>(null)
  const editorRef = useRef<editor.IStandaloneCodeEditor | null>(null)
  // Command palette (t11) + mobile single-pane tab selector (t11).
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [mobileTab, setMobileTab] = useState<"files" | "editor" | "preview" | "validate">("editor")

  function selectFile(path: string) {
    const f = files.find((x) => x.path === path)
    setSelected(path)
    setContent(f?.content ?? "")
    setDirty(false)
  }

  function jumpToIssue(path: string, line?: number, col?: number) {
    if (path && path !== selected) {
      selectFile(path)
    }
    // 切文件后 Monaco 需要一拍重新挂载/换 model，用 requestAnimationFrame 等它就绪
    requestAnimationFrame(() => {
      const ed = editorRef.current
      if (!ed || !line) return
      const column = col && col > 0 ? col : 1
      ed.revealLineInCenter(line)
      ed.setPosition({ lineNumber: line, column })
      ed.focus()
    })
  }

  // applyJsonTool formats or minifies the current JSON buffer in place.
  // It only rewrites the editor content on success; a parse failure is
  // surfaced as an error and the buffer is left untouched.
  function applyJsonTool(mode: "format" | "minify") {
    const res = mode === "format" ? formatJson(content) : minifyJson(content)
    if (!res.ok) {
      setError(res.error)
      return
    }
    if (res.text !== content) {
      setContent(res.text)
      setDirty(true)
    }
    setError(null)
  }

  // convertAndSave re-saves the current file with convert_to_utf8 so the
  // backend strips a UTF-8 BOM (the only conversion §1.3.8 permits without
  // it being a silent rewrite). The editor's in-memory text is already
  // decoded; this realigns the stored bytes and refreshes file metadata.
  async function convertAndSave() {
    if (!selected) return
    setBusy(true)
    try {
      const saved = await api.putDraftFile(draft.id, selected, content, true)
      setFiles((prev) => prev.map((f) => (f.path === selected ? { ...saved, content } : f)))
      setDirty(false)
      setError(null)
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to convert file."))
    } finally {
      setBusy(false)
    }
  }


  async function save() {
    if (!selected) return
    setBusy(true)
    try {
      await api.putDraftFile(draft.id, selected, content)
      setFiles((prev) => prev.map((f) => (f.path === selected ? { ...f, content } : f)))
      setDirty(false)
      setError(null)
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to save file."))
    } finally {
      setBusy(false)
    }
  }

  async function addFile() {
    const path = newPath.trim()
    if (!path) return
    setBusy(true)
    try {
      const f = await api.createDraftFile(draft.id, path, "")
      setFiles((prev) => [...prev, { ...f, content: "" }].sort((a, b) => a.path.localeCompare(b.path)))
      setNewPath("")
      selectFile(path)
      setError(null)
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to add file."))
    } finally {
      setBusy(false)
    }
  }

  async function removeFile(path: string) {
    setBusy(true)
    try {
      await api.deleteDraftFile(draft.id, path)
      const remaining = files.filter((f) => f.path !== path)
      setFiles(remaining)
      if (selected === path) selectFile(remaining[0]?.path ?? "")
      setError(null)
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to delete file."))
    } finally {
      setBusy(false)
    }
  }

  // renameFile composes existing APIs (no dedicated backend route):
  // create at the new path first (safefs validates it server-side),
  // then delete the old. Create-before-delete avoids losing content if
  // the new path is rejected.
  async function renameFile(oldPath: string, nextPath: string) {
    const target = nextPath.trim()
    if (!target || target === oldPath) return
    const src = files.find((f) => f.path === oldPath)
    const body = src?.content ?? ""
    setBusy(true)
    try {
      const created = await api.createDraftFile(draft.id, target, body)
      await api.deleteDraftFile(draft.id, oldPath)
      setFiles((prev) =>
        prev
          .filter((f) => f.path !== oldPath)
          .concat({ ...created, content: body })
          .sort((a, b) => a.path.localeCompare(b.path)),
      )
      selectFile(target)
      setError(null)
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to rename file."))
    } finally {
      setBusy(false)
    }
  }

  // downloadFile saves the (text) content of a file to the client. Binary
  // file bytes aren't inlined in the draft view, so this serves the text
  // we have; full binary download arrives with t10.
  function downloadFile(path: string) {
    const f = files.find((x) => x.path === path)
    const blob = new Blob([f?.content ?? ""], { type: "text/plain;charset=utf-8" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = path.split("/").pop() || path
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  }

  async function onUploadPicked(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = "" // allow re-picking the same file
    if (!file) return
    const path = file.name
    setBusy(true)
    try {
      const text = await file.text()
      const exists = files.some((f) => f.path === path)
      const saved = exists
        ? await api.putDraftFile(draft.id, path, text)
        : await api.createDraftFile(draft.id, path, text)
      setFiles((prev) =>
        prev
          .filter((f) => f.path !== path)
          .concat({ ...saved, content: text })
          .sort((a, b) => a.path.localeCompare(b.path)),
      )
      selectFile(path)
      setError(null)
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to upload file."))
    } finally {
      setBusy(false)
    }
  }

  async function publish() {
    setBusy(true)
    try {
      const res = await api.validateDraft(draft.id)
      setIssues(res.issues)
      if (!res.ok) {
        setBusy(false)
        return
      }
      await api.publishDraft(draft.id)
      onPublished()
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to publish."))
      setBusy(false)
    }
  }

  async function discard() {
    setBusy(true)
    try {
      await api.deleteDraft(draft.id)
      onDiscarded()
    } catch (err) {
      setError(apiErrorMessage(err, "Failed to discard draft."))
      setBusy(false)
    }
  }

  function startRename(path: string) {
    setRenaming(path)
    setRenameTo(path)
  }

  const current = files.find((f) => f.path === selected)
  const editable = current ? !current.is_binary : false
  const isMarkdown = /\.(md|markdown)$/i.test(selected)
  const isJson = /\.json$/i.test(selected)
  // Encoding badge / convert affordance (t9). The backend tags each file
  // utf-8 / utf-8-bom / binary; a BOM is the one case we offer to convert
  // (stripping it) without it counting as a silent rewrite (§1.3.8).
  const encoding = current?.encoding
  const hasBom = encoding === "utf-8-bom"

  // Global shortcuts (t11): Cmd/Ctrl+K opens the palette, Cmd/Ctrl+S
  // saves, Cmd/Ctrl+Enter validates+publishes. Re-bound when the gating
  // flags change so the handler never acts on stale enablement.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const mod = e.metaKey || e.ctrlKey
      if (!mod) return
      if (e.key === "k" || e.key === "K") {
        e.preventDefault()
        setPaletteOpen((o) => !o)
      } else if (e.key === "s" || e.key === "S") {
        e.preventDefault()
        if (dirty && editable && !busy) void save()
      } else if (e.key === "Enter") {
        e.preventDefault()
        if (!busy) void publish()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dirty, editable, busy])

  // Actions surfaced in the command palette. Disabled flags mirror the
  // toolbar so the palette can't trigger something the UI forbids.
  // Imperatively open the hidden file picker. Wrapped in a named
  // function so the ref access lives outside render scope (satisfies
  // react-hooks/refs) and can be shared by the toolbar and palette.
  function triggerUpload() {
    uploadRef.current?.click()
  }

  const paletteActions: CommandAction[] = [
    { id: "save", label: "Save file", keywords: "保存 write", shortcut: "⌘S", run: () => void save(), disabled: busy || !dirty || !editable },
    { id: "publish", label: "Validate & publish", keywords: "校验 发布 validate publish", shortcut: "⌘↵", run: () => void publish(), disabled: busy },
    { id: "newfile", label: "New file", keywords: "新建 文件 add create", run: () => document.getElementById("sf-newfile-input")?.focus() },
    { id: "upload", label: "Upload file", keywords: "上传 import", run: triggerUpload },
    { id: "discard", label: "Discard draft", keywords: "丢弃 删除 delete cancel", run: () => void discard(), disabled: busy },
    ...(isJson && editable
      ? [
          { id: "json-format", label: "Format JSON", keywords: "格式化 美化 pretty", run: () => applyJsonTool("format") },
          { id: "json-minify", label: "Minify JSON", keywords: "压缩 compress", run: () => applyJsonTool("minify") },
        ]
      : []),
    ...(hasBom && editable
      ? [{ id: "convert-utf8", label: "Convert to UTF-8 (strip BOM)", keywords: "编码 转换 encoding bom", run: () => void convertAndSave(), disabled: busy }]
      : []),
  ]

  // The three panes are built once and shared by the desktop SplitPane
  // and the mobile single-pane tab view (t11), so their markup isn't
  // duplicated across layouts.
  const leftPane = (
    <FileTreePanel
      files={files}
      selected={selected}
      busy={busy}
      newPath={newPath}
      onSelect={(p) => {
        selectFile(p)
        setMobileTab("editor")
      }}
      onNewPathChange={setNewPath}
      onAddFile={addFile}
      onRename={startRename}
      onDelete={(p) => setDeleting(p)}
      onDownload={downloadFile}
      onUploadClick={triggerUpload}
    />
  )

  const centerPane = (
    <div className="flex h-full flex-col">
      <div className="border-border flex items-center justify-between gap-2 border-b px-2 py-1.5">
        <div className="flex min-w-0 items-center gap-2">
          <span className="text-muted-foreground truncate font-mono text-xs" title={selected}>
            {selected || "—"}
            {dirty && <span className="ml-1 text-amber-500">●</span>}
          </span>
          {encoding && (
            <span
              className={
                "shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium " +
                (hasBom
                  ? "bg-amber-500/15 text-amber-600"
                  : "bg-muted text-muted-foreground")
              }
              title={hasBom ? "File has a UTF-8 byte-order mark" : `Encoding: ${encoding}`}
            >
              {encoding}
            </span>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {isJson && editable && (
            <>
              <Button size="sm" variant="ghost" onClick={() => applyJsonTool("format")} disabled={busy} title="Format JSON">
                <Braces className="size-3.5" aria-hidden />
                Format
              </Button>
              <Button size="sm" variant="ghost" onClick={() => applyJsonTool("minify")} disabled={busy} title="Minify JSON">
                <Minimize2 className="size-3.5" aria-hidden />
                Minify
              </Button>
            </>
          )}
          {hasBom && editable && (
            <Button size="sm" variant="ghost" onClick={convertAndSave} disabled={busy} title="Strip the BOM and save as UTF-8">
              <FileCode className="size-3.5" aria-hidden />
              Convert to UTF-8
            </Button>
          )}
          <Button size="sm" variant="secondary" onClick={save} disabled={busy || !dirty || !editable}>
            <Save className="size-3.5" aria-hidden />
            {dirty ? "Save" : "Saved"}
          </Button>
        </div>
      </div>
      <div className="min-h-0 flex-1">
        {current && editable ? (
          <MonacoFileEditor
            path={selected}
            value={content}
            onChange={(v) => {
              setContent(v)
              setDirty(true)
            }}
            onReady={(ed) => { editorRef.current = ed }}
            height="100%"
          />
        ) : current ? (
          <BinaryFileView file={current} onDownload={() => downloadFile(current.path)} />
        ) : (
          <p className="text-muted-foreground p-3 text-xs">Select a file to edit.</p>
        )}
      </div>
    </div>
  )

  const rightPane = (
    <div className="flex h-full flex-col">
      {isMarkdown && (
        <div className="border-border min-h-0 flex-1 overflow-auto border-b p-3">
          <div className="text-muted-foreground mb-2 text-[11px] font-semibold uppercase tracking-wide">
            Preview
          </div>
          <MarkdownPreview source={content} />
        </div>
      )}
      <div className={isMarkdown ? "max-h-48 shrink-0 overflow-auto p-2" : "min-h-0 flex-1 overflow-auto p-2"}>
        <ValidationPanel issues={issues} onJump={jumpToIssue} />
      </div>
    </div>
  )

  return (
    <div className="border-border space-y-3 rounded-md border p-3">
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium">
          Draft <span className="text-muted-foreground font-mono text-xs">{draft.id}</span>
        </span>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={publish} disabled={busy}>
            <Send className="size-4" aria-hidden />
            Validate &amp; publish
          </Button>
          <Button size="sm" variant="ghost" onClick={discard} disabled={busy}>
            <Trash2 className="size-4" aria-hidden />
            Discard
          </Button>
        </div>
      </div>

      {error && <p className="text-xs text-red-600">{error}</p>}

      {/* Desktop: three-pane resizable split. */}
      <div className="hidden h-[32rem] md:block">
        <SplitPane left={leftPane} center={centerPane} right={rightPane} />
      </div>

      {/* Mobile: a single pane at a time, chosen by a four-tab bar (t11). */}
      <div className="md:hidden">
        <div role="tablist" aria-label="Editor panes" className="border-border grid grid-cols-4 gap-1 border-b pb-2">
          {(["files", "editor", "preview", "validate"] as const).map((tab) => (
            <button
              key={tab}
              role="tab"
              aria-selected={mobileTab === tab}
              onClick={() => setMobileTab(tab)}
              className={
                "rounded px-2 py-1 text-xs capitalize " +
                (mobileTab === tab
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-muted")
              }
            >
              {tab}
            </button>
          ))}
        </div>
        <div className="h-[28rem] overflow-auto">
          {mobileTab === "files" && leftPane}
          {mobileTab === "editor" && centerPane}
          {mobileTab === "preview" && (
            isMarkdown ? (
              <div className="p-3">
                <MarkdownPreview source={content} />
              </div>
            ) : (
              <p className="text-muted-foreground p-3 text-xs">Preview is available for Markdown files.</p>
            )
          )}
          {mobileTab === "validate" && (
            <div className="p-2">
              <ValidationPanel issues={issues} onJump={jumpToIssue} />
            </div>
          )}
        </div>
      </div>

      <input ref={uploadRef} type="file" className="hidden" onChange={onUploadPicked} />

      {/* Rename dialog */}
      <Dialog open={renaming !== null} onOpenChange={(o) => !o && setRenaming(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Rename file</DialogTitle>
            <DialogDescription>Enter a new package-relative path.</DialogDescription>
          </DialogHeader>
          <Input
            value={renameTo}
            onChange={(e) => setRenameTo(e.target.value)}
            className="font-mono text-xs"
            onKeyDown={(e) => {
              if (e.key === "Enter" && renaming) {
                const from = renaming
                setRenaming(null)
                void renameFile(from, renameTo)
              }
            }}
          />
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setRenaming(null)}>
              Cancel
            </Button>
            <Button
              size="sm"
              disabled={busy || !renameTo.trim()}
              onClick={() => {
                if (!renaming) return
                const from = renaming
                setRenaming(null)
                void renameFile(from, renameTo)
              }}
            >
              Rename
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirm dialog */}
      <Dialog open={deleting !== null} onOpenChange={(o) => !o && setDeleting(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete file</DialogTitle>
            <DialogDescription>
              Delete <span className="font-mono">{deleting}</span>? This cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="ghost" size="sm" onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              disabled={busy}
              onClick={() => {
                if (!deleting) return
                const p = deleting
                setDeleting(null)
                void removeFile(p)
              }}
            >
              Delete
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} actions={paletteActions} />
    </div>
  )
}

// FileTreePanel is the left pane: the draft's file list with a context
// menu (rename/download/delete), keyboard shortcuts (Enter=rename,
// Delete=delete), an upload button, and a new-file input.
function FileTreePanel({
  files,
  selected,
  busy,
  newPath,
  onSelect,
  onNewPathChange,
  onAddFile,
  onRename,
  onDelete,
  onDownload,
  onUploadClick,
}: {
  files: Draft["files"]
  selected: string
  busy: boolean
  newPath: string
  onSelect: (path: string) => void
  onNewPathChange: (v: string) => void
  onAddFile: () => void
  onRename: (path: string) => void
  onDelete: (path: string) => void
  onDownload: (path: string) => void
  onUploadClick: () => void
}) {
  return (
    <div className="flex h-full flex-col gap-1 p-2">
      <div className="flex items-center justify-between">
        <span className="text-muted-foreground text-xs font-semibold uppercase tracking-wide">Files</span>
        <Button size="sm" variant="ghost" onClick={onUploadClick} disabled={busy} aria-label="Upload file">
          <Upload className="size-3.5" aria-hidden />
        </Button>
      </div>
      <ul className="min-h-0 flex-1 space-y-0.5 overflow-auto">
        {files.map((f) => (
          <li key={f.path}>
            <ContextMenu>
              <ContextMenuTrigger asChild>
                <button
                  className={`w-full truncate rounded px-1.5 py-0.5 text-left font-mono text-xs hover:bg-muted/50 ${
                    selected === f.path ? "bg-muted font-semibold" : ""
                  }`}
                  onClick={() => onSelect(f.path)}
                  title={f.path}
                  onKeyDown={(e) => {
                    if (e.key === "Enter") {
                      e.preventDefault()
                      onRename(f.path)
                    } else if (e.key === "Delete") {
                      e.preventDefault()
                      onDelete(f.path)
                    }
                  }}
                >
                  {f.path}
                </button>
              </ContextMenuTrigger>
              <ContextMenuContent>
                <ContextMenuItem onSelect={() => onRename(f.path)}>Rename</ContextMenuItem>
                <ContextMenuItem onSelect={() => onDownload(f.path)}>Download</ContextMenuItem>
                <ContextMenuSeparator />
                <ContextMenuItem variant="destructive" onSelect={() => onDelete(f.path)}>
                  Delete
                </ContextMenuItem>
              </ContextMenuContent>
            </ContextMenu>
          </li>
        ))}
      </ul>
      <div className="flex items-center gap-1 pt-1">
        <Input
          id="sf-newfile-input"
          value={newPath}
          onChange={(e) => onNewPathChange(e.target.value)}
          placeholder="new/file.md"
          className="h-7 font-mono text-xs"
          onKeyDown={(e) => {
            if (e.key === "Enter") onAddFile()
          }}
        />
        <Button size="sm" variant="ghost" onClick={onAddFile} disabled={busy || !newPath.trim()}>
          <FilePlus className="size-3" aria-hidden />
        </Button>
      </div>
    </div>
  )
}
