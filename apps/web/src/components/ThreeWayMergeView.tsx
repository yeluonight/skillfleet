import { useState } from "react"
import { DiffEditor } from "@monaco-editor/react"
import {
  AlertCircle,
  CheckCircle2,
  CloudDownload,
  Fingerprint,
  Laptop,
  Loader2,
  RefreshCw,
  SkipForward,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { DiffStatusBadge, DiffStatusIcon } from "@/lib/diff-status"
import { formatBytes } from "@/lib/format"
import { languageForPath } from "@/lib/language-map"
import type { DiffFile, LocalSide, SHACompare, ThreeWayDiff } from "@/lib/api"

// MergeChoice records the user's intent for one changed file:
// "local" 采用本地 · "remote" 采用上游 · "base" 跳过保持 base。
// Phase 7 only records intent — writes back are deferred to Phase 8.
export type MergeChoice = "local" | "remote" | "base"

export type ThreeWayMergeViewProps = {
  diff: ThreeWayDiff | null
  loading: boolean
  error: string | null
  // 每文件的合并选择，父组件持有（受控）。key=file path, value=MergeChoice
  choices: Record<string, MergeChoice>
  onChoose: (path: string, choice: MergeChoice) => void
  onRefresh?: () => void
  // 主题由父组件传入（§5.5）。缺省时渲染期读取一次 <html>.dark，不自行监听。
  isDark?: boolean
}

// ThreeWayMergeView is a pure controlled surface — the three-way (base | local
// | remote) sibling of UpstreamDiffView. Parents own all API calls, CSRF,
// refresh state, and the per-file merge choices; this component only renders
// props and reports intent through onChoose.
//
// Phase 7 boundary: the local side carries only a content fingerprint (sha),
// never bytes (LocalSide.content_available is always false). So base↔remote
// gets a real line-level Monaco diff, while local is shown as a sha-relationship
// summary with an honest "needs device upload (Phase 8)" note.
export function ThreeWayMergeView({
  diff,
  loading,
  error,
  choices,
  onChoose,
  onRefresh,
  isDark,
}: ThreeWayMergeViewProps) {
  // No theme listener here (§5.5): prefer the prop; fall back to a one-shot
  // read of <html> so the editor still themes correctly when used standalone.
  const dark = isDark ?? document.documentElement.classList.contains("dark")
  const [userSelected, setUserSelected] = useState<string | null>(null)

  if (error) {
    return (
      <Alert variant="destructive">
        <AlertCircle className="size-4" aria-hidden />
        <AlertTitle>加载差异失败</AlertTitle>
        <AlertDescription>{error}</AlertDescription>
      </Alert>
    )
  }

  if (loading && !diff) {
    return <p className="text-muted-foreground text-sm">加载差异中…</p>
  }

  if (!diff) {
    return <p className="text-muted-foreground text-sm">尚未加载差异。</p>
  }

  if (!diff.has_remote_update) {
    const localChanged = diff.local?.vs_base === "different"
    return (
      <Card>
        <CardContent className="space-y-4 py-6">
          <div className="flex items-start gap-3">
            <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-emerald-600" aria-hidden />
            <div className="space-y-1">
              {localChanged ? (
                <>
                  <p className="text-sm font-medium">本地有修改，但上游暂无更新</p>
                  <p className="text-muted-foreground text-sm">
                    当前没有待处理的上游更新；待 Phase 8 支持设备上传本地内容后再做合并。
                  </p>
                </>
              ) : (
                <>
                  <p className="text-sm font-medium">当前没有待处理的上游更新</p>
                  <p className="text-muted-foreground text-sm">绑定后用「检查更新」拉取上游变化。</p>
                </>
              )}
            </div>
          </div>
          {diff.local ? <LocalSideSummary local={diff.local} /> : null}
        </CardContent>
      </Card>
    )
  }

  const files = diff.files
  // Derive selection during render — no setState-in-effect (§硬性约束 2).
  const selectedPath = userSelected ?? files[0]?.path ?? null
  const selected = selectedPath ? files.find((file) => file.path === selectedPath) : undefined

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="min-w-0 space-y-1">
            <CardTitle className="text-lg">三方合并</CardTitle>
            <CardDescription className="flex flex-wrap items-center gap-2">
              <span className="text-muted-foreground font-mono text-xs">
                base {shortId(diff.base_version_id)} → remote {shortId(diff.remote_version_id)}
              </span>
              <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-600">
                {files.length} 个变更文件
              </span>
              <span className="text-muted-foreground rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium">
                {diff.unchanged} 个未变更
              </span>
            </CardDescription>
          </div>
          {onRefresh ? (
            <Button type="button" size="sm" variant="ghost" onClick={onRefresh} disabled={loading}>
              {loading ? (
                <Loader2 className="size-4 animate-spin" aria-hidden />
              ) : (
                <RefreshCw className="size-4" aria-hidden />
              )}
              刷新
            </Button>
          ) : null}
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {diff.local ? <LocalSideSummary local={diff.local} /> : null}
        <div className="grid gap-3 md:grid-cols-[18rem_minmax(0,1fr)]">
          <DiffFileList
            files={files}
            selectedPath={selectedPath}
            choices={choices}
            onSelect={setUserSelected}
          />
          <SelectedDiffPanel
            selected={selected}
            isDark={dark}
            choice={selected ? choices[selected.path] : undefined}
            onChoose={onChoose}
          />
        </div>
      </CardContent>
    </Card>
  )
}

// LocalSideSummary surfaces the device copy honestly: only a fingerprint and
// its relationship to base/remote — no bytes (Phase 7), so no per-file diff.
function LocalSideSummary({ local }: { local: LocalSide }) {
  return (
    <section className="rounded-md border border-sky-500/30 bg-sky-500/5 p-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <span className="flex items-center gap-1.5 text-sm font-medium text-sky-600">
          <Laptop className="size-4" aria-hidden />
          本地副本
        </span>
        <span className="text-muted-foreground font-mono text-xs">
          {local.device_id} · {local.tool_key} · {local.scope}
        </span>
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs">
        <span className="flex items-center gap-1.5">
          <span className="text-muted-foreground">vs base</span>
          <SHACompareBadge value={local.vs_base} />
        </span>
        <span className="flex items-center gap-1.5">
          <span className="text-muted-foreground">vs remote</span>
          <SHACompareBadge value={local.vs_remote} />
        </span>
        <span className="text-muted-foreground flex items-center gap-1 font-mono">
          <Fingerprint className="size-3.5" aria-hidden />
          {local.sha ? shortId(local.sha) : "无指纹"}
        </span>
      </div>
      <p className="text-muted-foreground mt-2 text-xs">
        本地内容需设备上传（Phase 8）：当前仅有内容指纹，无法逐文件展示本地差异。
      </p>
    </section>
  )
}

function DiffFileList({
  files,
  selectedPath,
  choices,
  onSelect,
}: {
  files: DiffFile[]
  selectedPath: string | null
  choices: Record<string, MergeChoice>
  onSelect: (path: string) => void
}) {
  return (
    <aside className="min-w-0 rounded-md border">
      <div className="border-b px-3 py-2">
        <h3 className="text-muted-foreground text-xs font-semibold tracking-wide uppercase">
          变更文件
        </h3>
      </div>
      {files.length === 0 ? (
        <p className="text-muted-foreground p-3 text-sm">没有变更文件。</p>
      ) : (
        <ul className="max-h-[32rem] space-y-0.5 overflow-auto p-2">
          {files.map((file) => {
            const active = file.path === selectedPath
            return (
              <li key={file.path}>
                <button
                  type="button"
                  className={
                    "focus-visible:ring-ring flex w-full min-w-0 items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-muted/50 focus-visible:ring-2 focus-visible:outline-none " +
                    (active ? "bg-muted font-semibold" : "")
                  }
                  onClick={() => onSelect(file.path)}
                  title={file.path}
                >
                  <DiffStatusIcon status={file.status} />
                  <DiffStatusBadge status={file.status} />
                  <span className="min-w-0 flex-1 truncate font-mono text-xs">{file.path}</span>
                  <ChoiceDot choice={choices[file.path]} />
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </aside>
  )
}

function SelectedDiffPanel({
  selected,
  isDark,
  choice,
  onChoose,
}: {
  selected?: DiffFile
  isDark: boolean
  choice?: MergeChoice
  onChoose: (path: string, choice: MergeChoice) => void
}) {
  return (
    <section className="min-w-0 rounded-md border">
      <div className="space-y-2 border-b px-3 py-2">
        {selected ? (
          <>
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <span className="min-w-0 truncate font-mono text-xs" title={selected.path}>
                {selected.path}
              </span>
              <DiffStatusBadge status={selected.status} />
              <span className="text-muted-foreground text-xs">
                base {formatBytes(selected.base_size)} → remote {formatBytes(selected.target_size)}
              </span>
            </div>
            <MergeChoiceButtons path={selected.path} current={choice} onChoose={onChoose} />
          </>
        ) : (
          <span className="text-muted-foreground text-sm">差异预览（base ↔ 上游）</span>
        )}
      </div>
      <div className="h-[28rem] md:h-[32rem]">
        {selected ? (
          selected.editable ? (
            <DiffEditor
              original={selected.base_content ?? ""}
              modified={selected.target_content ?? ""}
              language={languageForPath(selected.path)}
              theme={isDark ? "vs-dark" : "vs"}
              height="100%"
              loading={<span className="text-muted-foreground text-xs">加载 diff…</span>}
              options={{
                readOnly: true,
                renderSideBySide: true,
                minimap: { enabled: false },
                fontSize: 13,
                scrollBeyondLastLine: false,
                automaticLayout: true,
                fontFamily: "JetBrains Mono, Fira Code, ui-monospace, monospace",
              }}
            />
          ) : (
            <BinaryDiffPlaceholder file={selected} />
          )
        ) : (
          <div className="flex h-full items-center justify-center p-6">
            <p className="text-muted-foreground text-sm">选择左侧文件查看 base ↔ 上游 差异</p>
          </div>
        )}
      </div>
    </section>
  )
}

// MergeChoiceButtons renders the controlled three-way pick for one file. It
// only reports intent via onChoose; the parent owns the choices state.
function MergeChoiceButtons({
  path,
  current,
  onChoose,
}: {
  path: string
  current?: MergeChoice
  onChoose: (path: string, choice: MergeChoice) => void
}) {
  const options: MergeChoice[] = ["local", "remote", "base"]
  return (
    <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="合并选择">
      {options.map((option) => {
        const meta = choiceMeta(option)
        const active = current === option
        const Icon = meta.Icon
        return (
          <Button
            key={option}
            type="button"
            size="sm"
            variant="outline"
            aria-pressed={active}
            className={active ? meta.activeClass : "text-muted-foreground"}
            onClick={() => onChoose(path, option)}
          >
            <Icon className="size-3.5" aria-hidden />
            {meta.label}
          </Button>
        )
      })}
    </div>
  )
}

function ChoiceDot({ choice }: { choice?: MergeChoice }) {
  if (!choice) {
    return null
  }
  const meta = choiceMeta(choice)
  return (
    <span className={`shrink-0 rounded px-1.5 py-0.5 text-[9px] font-medium ${meta.dotClass}`}>
      {meta.short}
    </span>
  )
}

function BinaryDiffPlaceholder({ file }: { file: DiffFile }) {
  return (
    <div className="flex h-full items-center justify-center p-6">
      <div className="max-w-sm rounded-md border bg-muted/30 px-4 py-4 text-center">
        <AlertCircle className="text-muted-foreground mx-auto size-5" aria-hidden />
        <p className="mt-2 text-sm font-medium">二进制或超大文件，无法显示行级差异</p>
        <dl className="mt-3 grid grid-cols-[5rem_1fr] gap-x-3 gap-y-1 text-left text-xs">
          <dt className="text-muted-foreground">base</dt>
          <dd className="font-mono">
            {file.base_present ? formatBytes(file.base_size) : `不存在 · ${formatBytes(file.base_size)}`}
          </dd>
          <dt className="text-muted-foreground">remote</dt>
          <dd className="font-mono">
            {file.target_present
              ? formatBytes(file.target_size)
              : `不存在 · ${formatBytes(file.target_size)}`}
          </dd>
        </dl>
      </div>
    </div>
  )
}

function SHACompareBadge({ value }: { value: SHACompare }) {
  let label: string
  let colour: string
  switch (value) {
    case "same":
      label = "一致"
      colour = "bg-emerald-500/15 text-emerald-600"
      break
    case "different":
      label = "不同"
      colour = "bg-amber-500/15 text-amber-600"
      break
    case "unknown":
      label = "未知"
      colour = "bg-muted text-muted-foreground"
      break
  }
  return (
    <span className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${colour}`}>
      {label}
    </span>
  )
}

function choiceMeta(choice: MergeChoice): {
  label: string
  short: string
  Icon: typeof Laptop
  activeClass: string
  dotClass: string
} {
  switch (choice) {
    case "local":
      return {
        label: "采用本地",
        short: "本地",
        Icon: Laptop,
        activeClass: "border-sky-500 bg-sky-500/10 text-sky-600 hover:bg-sky-500/15",
        dotClass: "bg-sky-500/15 text-sky-600",
      }
    case "remote":
      return {
        label: "采用上游",
        short: "上游",
        Icon: CloudDownload,
        activeClass: "border-emerald-500 bg-emerald-500/10 text-emerald-600 hover:bg-emerald-500/15",
        dotClass: "bg-emerald-500/15 text-emerald-600",
      }
    case "base":
      return {
        label: "跳过(保持base)",
        short: "保持",
        Icon: SkipForward,
        activeClass: "border-muted-foreground/40 bg-muted text-foreground hover:bg-muted",
        dotClass: "bg-muted text-muted-foreground",
      }
  }
}

function shortId(id: string | undefined): string {
  return id ? id.slice(0, 8) : "—"
}
