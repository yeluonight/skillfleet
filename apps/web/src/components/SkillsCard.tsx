import { useRef, useState } from "react"
import { AlertCircle, ChevronDown, ChevronRight, FileText, Link2, Package, Plus, Upload } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Input } from "@/components/ui/input"

import { api } from "@/lib/api"
import type { SkillSummary } from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"
import { useAsyncAction } from "@/hooks/useAsyncAction"
import { SkillDetailPanel } from "@/components/SkillDetailPanel"

// SkillsCard is the Registry surface (v1.0 §13.3): a list of skills,
// a create form, a zip importer, and per-skill expansion into versions
// + file tree + a plain draft editor (Monaco lands in Phase 5).
export function SkillsCard() {
  const {
    data: skillsData,
    error: loadError,
    refresh,
    setError: setLoadError,
  } = useApiResource<{ skills: SkillSummary[] }>(() => api.listSkills(), {
    errorFallback: "Failed to load skills.",
  })
  const skills = skillsData?.skills ?? null
  const [expanded, setExpanded] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [newName, setNewName] = useState("")
  const action = useAsyncAction()
  const fileInput = useRef<HTMLInputElement>(null)
  // Load errors and action errors share one banner; an action clears the
  // load error when it starts (useAsyncAction.run clears its own error).
  const error = action.error ?? loadError

  async function handleCreate() {
    const name = newName.trim()
    if (!name) return
    setLoadError(null)
    const ok = await action.run(() => api.createSkill(name), "Failed to create skill.")
    if (ok) {
      setNewName("")
      setCreating(false)
      await refresh()
    }
  }

  async function handleImport(file: File) {
    setLoadError(null)
    await action.run(() => api.importSkillZip(file), "Failed to import zip.")
    if (fileInput.current) fileInput.current.value = ""
    await refresh()
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg flex items-center gap-2">
          <Package className="text-primary size-5" aria-hidden />
          Skills registry
        </CardTitle>
        <CardDescription>
          Multi-file Skill packages with immutable versions. Create a blank skill, import a zip, or
          fork a version into an editable draft and publish a new version.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <Button size="sm" variant={creating ? "secondary" : "default"} onClick={() => setCreating((v) => !v)}>
              <Plus className="size-4" aria-hidden />
              New skill
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={action.busy}
              onClick={() => fileInput.current?.click()}
            >
              <Upload className="size-4" aria-hidden />
              Import zip
            </Button>
            <input
              ref={fileInput}
              type="file"
              accept=".zip,application/zip"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0]
                if (f) void handleImport(f)
              }}
            />
          </div>
        </div>

        {creating && (
          <div className="flex items-center gap-2">
            <Input
              autoFocus
              placeholder="skill-name"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void handleCreate()
                if (e.key === "Escape") setCreating(false)
              }}
              className="max-w-xs"
            />
            <Button size="sm" onClick={handleCreate} disabled={action.busy || !newName.trim()}>
              {action.busy ? "Creating…" : "Create"}
            </Button>
          </div>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertCircle className="size-4" aria-hidden />
            <AlertTitle>Registry error</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}

        <SkillList
          skills={skills}
          expanded={expanded}
          onToggle={(name) => setExpanded((cur) => (cur === name ? null : name))}
          onChanged={refresh}
        />
      </CardContent>
    </Card>
  )
}

function SkillList({
  skills,
  expanded,
  onToggle,
  onChanged,
}: {
  skills: SkillSummary[] | null
  expanded: string | null
  onToggle: (name: string) => void
  onChanged: () => void
}) {
  if (skills === null) {
    return <p className="text-muted-foreground text-sm">Loading skills…</p>
  }
  if (skills.length === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        No skills yet. Create a blank skill or import a zip to get started.
      </p>
    )
  }
  return (
    <ul className="divide-border divide-y rounded-md border">
      {skills.map((s) => {
        const open = expanded === s.name
        return (
          <li key={s.name} className="px-3 py-3">
            <div className="flex items-center justify-between gap-4">
              <button
                className="flex items-center gap-2 text-left"
                onClick={() => onToggle(s.name)}
                aria-expanded={open}
              >
                {open ? (
                  <ChevronDown className="size-4" aria-hidden />
                ) : (
                  <ChevronRight className="size-4" aria-hidden />
                )}
                <FileText className="text-muted-foreground size-4" aria-hidden />
                <span className="text-sm font-medium">{s.name}</span>
                {s.source_state === "bound" ? (
                  <span
                    className="inline-flex items-center gap-1 rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-medium text-emerald-600"
                    title="已绑定上游来源"
                  >
                    <Link2 className="size-3" aria-hidden />
                    bound
                  </span>
                ) : null}
              </button>
              <div className="text-muted-foreground text-xs">
                {s.version_count} version{s.version_count === 1 ? "" : "s"} · {s.latest_kind}
              </div>
            </div>
            {open ? <SkillDetailPanel name={s.name} onChanged={onChanged} /> : null}
          </li>
        )
      })}
    </ul>
  )
}
