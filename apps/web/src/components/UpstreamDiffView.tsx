import { useEffect, useState } from "react"
import { DiffEditor } from "@monaco-editor/react"
import { AlertCircle, CheckCircle2, Loader2, RefreshCw } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { DiffStatusBadge, DiffStatusIcon } from "@/lib/diff-status"
import { formatBytes } from "@/lib/format"
import { languageForPath } from "@/lib/language-map"
import type { DiffFile, UpstreamDiff } from "@/lib/api"

export type UpstreamDiffViewProps = {
  diff: UpstreamDiff | null
  loading: boolean
  error: string | null
  onRefresh?: () => void
}

// UpstreamDiffView is a pure controlled surface. Parents own API calls,
// CSRF, refresh state, and error handling; this component only renders props.
export function UpstreamDiffView({ diff, loading, error, onRefresh }: UpstreamDiffViewProps) {
  const [isDark, setIsDark] = useState<boolean>(
    () => document.documentElement.classList.contains("dark"),
  )
  const [userSelected, setUserSelected] = useState<string | null>(null)

  // Watch <html> class changes so Monaco DiffEditor follows the app theme.
  useEffect(() => {
    const observer = new MutationObserver(() => {
      setIsDark(document.documentElement.classList.contains("dark"))
    })
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    })
    return () => observer.disconnect()
  }, [])

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

  if (!diff.has_update) {
    return (
      <Card>
        <CardContent className="flex items-start gap-3 py-6">
          <CheckCircle2 className="mt-0.5 size-5 shrink-0 text-emerald-600" aria-hidden />
          <div className="space-y-1">
            <p className="text-sm font-medium">当前没有待处理的上游更新</p>
            <p className="text-muted-foreground text-sm">绑定后用「检查更新」拉取上游变化。</p>
          </div>
        </CardContent>
      </Card>
    )
  }

  const files = diff.files
  const selectedPath = userSelected ?? files[0]?.path ?? null
  const selected = selectedPath ? files.find((file) => file.path === selectedPath) : undefined

  return (
    <Card>
      <CardHeader>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
          <div className="min-w-0 space-y-1">
            <CardTitle className="text-lg">上游差异</CardTitle>
            <CardDescription className="flex flex-wrap items-center gap-2">
              <span className="text-muted-foreground font-mono text-xs">
                {shortId(diff.base_version_id)} → {shortId(diff.target_version_id)}
              </span>
              <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[10px] font-medium text-amber-600">
                {files.length} 个变更文件
              </span>
              <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
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
      <CardContent>
        <div className="grid gap-3 md:grid-cols-[18rem_minmax(0,1fr)]">
          <DiffFileList files={files} selectedPath={selectedPath} onSelect={setUserSelected} />
          <SelectedDiffPanel selected={selected} isDark={isDark} />
        </div>
      </CardContent>
    </Card>
  )
}

function DiffFileList({
  files,
  selectedPath,
  onSelect,
}: {
  files: DiffFile[]
  selectedPath: string | null
  onSelect: (path: string) => void
}) {
  return (
    <aside className="min-w-0 rounded-md border">
      <div className="border-b px-3 py-2">
        <h3 className="text-muted-foreground text-xs font-semibold uppercase tracking-wide">Files</h3>
      </div>
      {files.length === 0 ? (
        <p className="text-muted-foreground p-3 text-sm">没有变更文件。</p>
      ) : (
        <ul className="max-h-[32rem] space-y-0.5 overflow-auto p-2">
          {files.map((file) => {
            const selected = file.path === selectedPath
            return (
              <li key={file.path}>
                <button
                  type="button"
                  className={
                    "flex w-full min-w-0 items-center gap-2 rounded px-2 py-1.5 text-left hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring " +
                    (selected ? "bg-muted font-semibold" : "")
                  }
                  onClick={() => onSelect(file.path)}
                  title={file.path}
                >
                  <DiffStatusIcon status={file.status} />
                  <DiffStatusBadge status={file.status} />
                  <span className="min-w-0 flex-1 truncate font-mono text-xs">{file.path}</span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </aside>
  )
}

function SelectedDiffPanel({ selected, isDark }: { selected?: DiffFile; isDark: boolean }) {
  return (
    <section className="min-w-0 rounded-md border">
      <div className="border-b px-3 py-2">
        {selected ? (
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <span className="min-w-0 truncate font-mono text-xs" title={selected.path}>
              {selected.path}
            </span>
            <DiffStatusBadge status={selected.status} />
            <span className="text-muted-foreground text-xs">
              {formatBytes(selected.base_size)} → {formatBytes(selected.target_size)}
            </span>
          </div>
        ) : (
          <span className="text-muted-foreground text-sm">差异预览</span>
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
            <p className="text-muted-foreground text-sm">选择左侧文件查看差异</p>
          </div>
        )}
      </div>
    </section>
  )
}

function BinaryDiffPlaceholder({ file }: { file: DiffFile }) {
  return (
    <div className="flex h-full items-center justify-center p-6">
      <div className="max-w-sm rounded-md border bg-muted/30 px-4 py-4 text-center">
        <AlertCircle className="mx-auto size-5 text-muted-foreground" aria-hidden />
        <p className="mt-2 text-sm font-medium">二进制或超大文件，无法显示行级差异</p>
        <dl className="mt-3 grid grid-cols-[5rem_1fr] gap-x-3 gap-y-1 text-left text-xs">
          <dt className="text-muted-foreground">baseline</dt>
          <dd className="font-mono">
            {file.base_present ? formatBytes(file.base_size) : `不存在 · ${formatBytes(file.base_size)}`}
          </dd>
          <dt className="text-muted-foreground">pending</dt>
          <dd className="font-mono">
            {file.target_present ? formatBytes(file.target_size) : `不存在 · ${formatBytes(file.target_size)}`}
          </dd>
        </dl>
      </div>
    </div>
  )
}

function shortId(id: string | undefined): string {
  return id ? id.slice(0, 8) : "—"
}
