import { useState, type ReactNode } from "react"
import { AlertCircle, Check, ChevronRight, Eye, GitBranch, Loader2 } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { formatBytes } from "@/lib/format"
import type { BindPreview, BindPreviewFile, BindSourceParams } from "@/lib/api"

export type BindWizardProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  onPreview: (params: BindSourceParams) => void
  onConfirm: (params: BindSourceParams) => void
  // onBack discards a fetched preview to return to the form step. The parent
  // owns the preview state, so clearing it is its responsibility.
  onBack: () => void
  preview: BindPreview | null
  previewError: string | null
  busy: boolean
}

// manualStep is the step the user has navigated to by clicking. The preview
// step (3) is NOT stored: it's derived from "we're on the form step AND a
// preview has arrived", so a fetched preview advances the view without an
// effect, and the parent clearing the preview (onBack) returns to the form.
type ManualStep = 1 | 2
// Step is the derived visible step including the preview step (3).
type Step = 1 | 2 | 3
type SourceType = "github_repo" | "git_repo" | "github_release"
type RefType = "branch" | "tag" | "commit" | "release"

const SOURCE_TYPES: { value: SourceType; label: string; help: string }[] = [
  { value: "github_repo", label: "GitHub 仓库", help: "从 GitHub 仓库导入 Skill" },
  { value: "git_repo", label: "Git 仓库", help: "从通用 Git URL 导入" },
  { value: "github_release", label: "GitHub Release", help: "从 Release 归档导入" },
]

const REF_TYPES: { value: RefType; label: string }[] = [
  { value: "branch", label: "branch" },
  { value: "tag", label: "tag" },
  { value: "commit", label: "commit" },
  { value: "release", label: "release" },
]

// BindWizard owns only form/display state. Preview and bind effects are
// delegated to the parent via callbacks so API/CSRF/error handling stays out.
export function BindWizard({
  open,
  onOpenChange,
  onPreview,
  onConfirm,
  onBack,
  preview,
  previewError,
  busy,
}: BindWizardProps) {
  const [manualStep, setManualStep] = useState<ManualStep>(1)
  const [sourceType, setSourceType] = useState<SourceType>("github_repo")
  const [url, setUrl] = useState("")
  const [refType, setRefType] = useState<RefType>("branch")
  const [refName, setRefName] = useState("")
  const [subdir, setSubdir] = useState("")
  const [provider, setProvider] = useState("")
  const [owner, setOwner] = useState("")
  const [repo, setRepo] = useState("")
  const [localError, setLocalError] = useState<string | null>(null)

  // The visible step is DERIVED, not stored: once the user is on the form
  // (step 2) and a preview has been fetched, show the preview step. Clearing
  // the preview (via onBack in the parent) drops us straight back to the
  // form without any effect-driven state sync.
  const step: Step = manualStep === 2 && preview ? 3 : manualStep

  function buildParams(): BindSourceParams {
    const params: BindSourceParams = {
      source_type: sourceType,
      url: url.trim(),
      ref_type: refType,
    }
    const cleanRefName = refName.trim()
    const cleanSubdir = subdir.trim()
    const cleanProvider = provider.trim()
    const cleanOwner = owner.trim()
    const cleanRepo = repo.trim()
    if (cleanRefName) params.ref_name = cleanRefName
    if (cleanSubdir) params.subdir = cleanSubdir
    if (cleanProvider) params.provider = cleanProvider
    if (cleanOwner) params.owner = cleanOwner
    if (cleanRepo) params.repo = cleanRepo
    return params
  }

  function requestPreview() {
    if (!url.trim()) {
      setLocalError("请填写来源 URL。")
      return
    }
    setLocalError(null)
    onPreview(buildParams())
  }

  function confirmBind() {
    if (!url.trim()) {
      setLocalError("请填写来源 URL。")
      return
    }
    setLocalError(null)
    onConfirm(buildParams())
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <GitBranch className="text-primary size-5" aria-hidden />
            绑定来源
          </DialogTitle>
          <DialogDescription>选择来源、填写定位信息，拉取预览后再确认绑定。</DialogDescription>
        </DialogHeader>

        <StepIndicator step={step} />

        {step === 1 ? (
          <SourceTypeStep
            sourceType={sourceType}
            busy={busy}
            onChange={setSourceType}
            onNext={() => setManualStep(2)}
          />
        ) : step === 2 ? (
          <SourceFormStep
            sourceType={sourceType}
            url={url}
            refType={refType}
            refName={refName}
            subdir={subdir}
            provider={provider}
            owner={owner}
            repo={repo}
            localError={localError}
            previewError={previewError}
            busy={busy}
            onUrlChange={setUrl}
            onRefTypeChange={setRefType}
            onRefNameChange={setRefName}
            onSubdirChange={setSubdir}
            onProviderChange={setProvider}
            onOwnerChange={setOwner}
            onRepoChange={setRepo}
            onBack={() => setManualStep(1)}
            onPreview={requestPreview}
          />
        ) : (
          <PreviewStep
            preview={preview as BindPreview}
            busy={busy}
            onBack={onBack}
            onConfirm={confirmBind}
          />
        )}
      </DialogContent>
    </Dialog>
  )
}

function StepIndicator({ step }: { step: Step }) {
  return (
    <div className="flex flex-wrap items-center gap-1 text-xs text-muted-foreground">
      <StepPill active={step === 1}>1 选类型</StepPill>
      <ChevronRight className="size-3.5" aria-hidden />
      <StepPill active={step === 2}>2 填信息</StepPill>
      <ChevronRight className="size-3.5" aria-hidden />
      <StepPill active={step === 3}>3 确认预览</StepPill>
    </div>
  )
}

function StepPill({ active, children }: { active: boolean; children: ReactNode }) {
  return (
    <span
      className={
        "rounded px-1.5 py-0.5 text-[10px] font-medium " +
        (active ? "bg-primary/15 text-primary" : "bg-muted text-muted-foreground")
      }
    >
      {children}
    </span>
  )
}

function SourceTypeStep({
  sourceType,
  busy,
  onChange,
  onNext,
}: {
  sourceType: SourceType
  busy: boolean
  onChange: (value: SourceType) => void
  onNext: () => void
}) {
  return (
    <div className="space-y-4">
      <div className="grid gap-2 sm:grid-cols-3">
        {SOURCE_TYPES.map((item) => {
          const selected = item.value === sourceType
          return (
            <button
              key={item.value}
              type="button"
              disabled={busy}
              onClick={() => onChange(item.value)}
              className={
                "rounded-lg border px-3 py-2 text-left transition-colors disabled:pointer-events-none disabled:opacity-50 " +
                (selected ? "border-primary bg-primary/10" : "border-border hover:bg-muted/50")
              }
              aria-pressed={selected}
            >
              <span className="block text-sm font-medium">{item.label}</span>
              <span className="text-muted-foreground mt-1 block text-xs">{item.help}</span>
            </button>
          )
        })}
      </div>
      <DialogFooter>
        <Button type="button" size="sm" onClick={onNext} disabled={busy}>
          下一步
        </Button>
      </DialogFooter>
    </div>
  )
}

function SourceFormStep({
  sourceType,
  url,
  refType,
  refName,
  subdir,
  provider,
  owner,
  repo,
  localError,
  previewError,
  busy,
  onUrlChange,
  onRefTypeChange,
  onRefNameChange,
  onSubdirChange,
  onProviderChange,
  onOwnerChange,
  onRepoChange,
  onBack,
  onPreview,
}: {
  sourceType: SourceType
  url: string
  refType: RefType
  refName: string
  subdir: string
  provider: string
  owner: string
  repo: string
  localError: string | null
  previewError: string | null
  busy: boolean
  onUrlChange: (value: string) => void
  onRefTypeChange: (value: RefType) => void
  onRefNameChange: (value: string) => void
  onSubdirChange: (value: string) => void
  onProviderChange: (value: string) => void
  onOwnerChange: (value: string) => void
  onRepoChange: (value: string) => void
  onBack: () => void
  onPreview: () => void
}) {
  return (
    <div className="space-y-4">
      <div className="rounded-md border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
        当前类型：<span className="font-medium text-foreground">{sourceTypeLabel(sourceType)}</span>
      </div>

      <div className="space-y-2">
        <Label htmlFor="sf-source-url">URL</Label>
        <Input
          id="sf-source-url"
          value={url}
          onChange={(e) => onUrlChange(e.target.value)}
          placeholder="https://github.com/owner/repo"
          disabled={busy}
          aria-invalid={Boolean(localError)}
        />
        {localError ? <p className="text-xs text-red-600">{localError}</p> : null}
      </div>

      <div className="grid gap-3 sm:grid-cols-[10rem_1fr]">
        <div className="space-y-2">
          <Label htmlFor="sf-ref-type">ref_type</Label>
          <select
            id="sf-ref-type"
            value={refType}
            onChange={(e) => onRefTypeChange(e.target.value as RefType)}
            disabled={busy}
            className="h-8 w-full rounded-lg border border-input bg-background px-2.5 py-1 text-sm outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50"
          >
            {REF_TYPES.map((item) => (
              <option key={item.value} value={item.value}>
                {item.label}
              </option>
            ))}
          </select>
        </div>
        <div className="space-y-2">
          <Label htmlFor="sf-ref-name">ref_name</Label>
          <Input
            id="sf-ref-name"
            value={refName}
            onChange={(e) => onRefNameChange(e.target.value)}
            placeholder="main"
            disabled={busy}
          />
          <p className="text-muted-foreground text-xs">branch 可留空，表示默认分支。</p>
        </div>
      </div>

      <div className="space-y-2">
        <Label htmlFor="sf-subdir">subdir</Label>
        <Input
          id="sf-subdir"
          value={subdir}
          onChange={(e) => onSubdirChange(e.target.value)}
          placeholder="path/to/skill"
          disabled={busy}
        />
        <p className="text-muted-foreground text-xs">可留空，表示仓库根目录。</p>
      </div>

      <div className="space-y-2 rounded-md border border-border p-3">
        <div className="text-muted-foreground text-xs font-semibold uppercase tracking-wide">
          可选 GitHub 坐标
        </div>
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="space-y-2">
            <Label htmlFor="sf-provider">provider</Label>
            <Input
              id="sf-provider"
              value={provider}
              onChange={(e) => onProviderChange(e.target.value)}
              placeholder="github"
              disabled={busy}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sf-owner">owner</Label>
            <Input
              id="sf-owner"
              value={owner}
              onChange={(e) => onOwnerChange(e.target.value)}
              placeholder="owner"
              disabled={busy}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="sf-repo">repo</Label>
            <Input
              id="sf-repo"
              value={repo}
              onChange={(e) => onRepoChange(e.target.value)}
              placeholder="repo"
              disabled={busy}
            />
          </div>
        </div>
      </div>

      {previewError ? (
        <Alert variant="destructive">
          <AlertCircle className="size-4" aria-hidden />
          <AlertTitle>预览失败</AlertTitle>
          <AlertDescription>{previewError}</AlertDescription>
        </Alert>
      ) : null}

      <DialogFooter>
        <Button type="button" variant="ghost" size="sm" onClick={onBack} disabled={busy}>
          上一步
        </Button>
        <Button type="button" size="sm" onClick={onPreview} disabled={busy || !url.trim()}>
          {busy ? <Loader2 className="size-4 animate-spin" aria-hidden /> : <Eye className="size-4" aria-hidden />}
          拉取预览
        </Button>
      </DialogFooter>
    </div>
  )
}

function PreviewStep({
  preview,
  busy,
  onBack,
  onConfirm,
}: {
  preview: BindPreview
  busy: boolean
  onBack: () => void
  onConfirm: () => void
}) {
  return (
    <div className="space-y-4">
      <div className="rounded-md border border-border p-3">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 space-y-1">
            <div className="flex items-center gap-2">
              {preview.has_skill_md ? (
                <Check className="size-4 text-emerald-600" aria-hidden />
              ) : (
                <AlertCircle className="size-4 text-amber-600" aria-hidden />
              )}
              <h3 className="text-sm font-medium">{preview.name || "未命名 Skill"}</h3>
            </div>
            {preview.description ? (
              <p className="text-muted-foreground text-sm">{preview.description}</p>
            ) : (
              <p className="text-muted-foreground text-sm">没有 description。</p>
            )}
          </div>
          <span
            className={
              "shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium " +
              (preview.has_skill_md
                ? "bg-emerald-500/15 text-emerald-600"
                : "bg-amber-500/15 text-amber-600")
            }
          >
            {preview.has_skill_md ? "SKILL.md" : "缺少 SKILL.md"}
          </span>
        </div>

        {!preview.has_skill_md ? (
          <div className="mt-3 rounded-md border border-amber-500/20 bg-amber-500/10 px-2.5 py-2 text-sm text-amber-700">
            未找到 SKILL.md。请返回修改 subdir 或来源定位信息。
          </div>
        ) : null}

        <dl className="mt-3 grid gap-2 text-sm sm:grid-cols-[8rem_1fr]">
          <PreviewInfo label="commit">
            <span className="font-mono text-xs">{preview.commit.slice(0, 12)}</span>
          </PreviewInfo>
          <PreviewInfo label="file_count">{preview.file_count}</PreviewInfo>
          <PreviewInfo label="total_bytes">{formatBytes(preview.total_bytes)}</PreviewInfo>
        </dl>
      </div>

      <div className="space-y-2">
        <div className="text-muted-foreground text-xs font-semibold uppercase tracking-wide">文件树</div>
        <FileTree files={preview.files} />
      </div>

      {preview.warnings && preview.warnings.length > 0 ? (
        <div className="space-y-1 rounded-md border border-amber-500/20 bg-amber-500/10 px-2.5 py-2 text-sm text-amber-700">
          <div className="font-medium">预览警告</div>
          <ul className="list-disc space-y-1 pl-4">
            {preview.warnings.map((warning, index) => (
              <li key={`${warning}-${index}`}>{warning}</li>
            ))}
          </ul>
        </div>
      ) : null}

      <DialogFooter>
        <Button type="button" variant="ghost" size="sm" onClick={onBack} disabled={busy}>
          返回修改
        </Button>
        <Button type="button" size="sm" onClick={onConfirm} disabled={busy}>
          {busy ? <Loader2 className="size-4 animate-spin" aria-hidden /> : null}
          确认绑定
        </Button>
      </DialogFooter>
    </div>
  )
}

function PreviewInfo({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground text-xs font-semibold uppercase tracking-wide">{label}</dt>
      <dd>{children}</dd>
    </>
  )
}

function FileTree({ files }: { files: BindPreviewFile[] }) {
  if (files.length === 0) {
    return <p className="text-muted-foreground text-sm">预览中没有文件。</p>
  }

  return (
    <ul className="max-h-48 space-y-0.5 overflow-auto rounded-md border border-border p-2">
      {files.map((file) => (
        <li key={file.path} className="flex items-center justify-between gap-3 text-xs">
          <span className="min-w-0 truncate font-mono" title={file.path}>
            {file.path}
          </span>
          <span className="text-muted-foreground shrink-0">
            {formatBytes(file.size)}{file.binary ? " · binary" : ""}
          </span>
        </li>
      ))}
    </ul>
  )
}

function sourceTypeLabel(sourceType: SourceType): string {
  return SOURCE_TYPES.find((item) => item.value === sourceType)?.label ?? sourceType
}
