import { useRef, useState } from "react"
import { AlertCircle, ChevronDown, ChevronRight, FileText, Link2, Package, Plus, Upload } from "lucide-react"
import { useTranslation } from "react-i18next"

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
  const { t } = useTranslation()
  const {
    data: skillsData,
    error: loadError,
    refresh,
    setError: setLoadError,
  } = useApiResource<{ skills: SkillSummary[] }>(() => api.listSkills(), {
    errorFallback: t("skills.err.loadSkills"),
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
    const ok = await action.run(() => api.createSkill(name), t("skills.err.createSkill"))
    if (ok) {
      setNewName("")
      setCreating(false)
      await refresh()
    }
  }

  async function handleImport(file: File) {
    setLoadError(null)
    await action.run(() => api.importSkillZip(file), t("skills.err.importZip"))
    if (fileInput.current) fileInput.current.value = ""
    await refresh()
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg flex items-center gap-2">
          <Package className="text-primary size-5" aria-hidden />
          {t("skills.registryTitle")}
        </CardTitle>
        <CardDescription>
          {t("skills.registryDesc")}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <Button size="sm" variant={creating ? "secondary" : "default"} onClick={() => setCreating((v) => !v)}>
              <Plus className="size-4" aria-hidden />
              {t("skills.newSkill")}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={action.busy}
              onClick={() => fileInput.current?.click()}
            >
              <Upload className="size-4" aria-hidden />
              {t("skills.importZip")}
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
              {action.busy ? t("skills.creating") : t("skills.create")}
            </Button>
          </div>
        )}

        {error && (
          <Alert variant="destructive">
            <AlertCircle className="size-4" aria-hidden />
            <AlertTitle>{t("skills.registryError")}</AlertTitle>
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
  const { t } = useTranslation()
  if (skills === null) {
    return <p className="text-muted-foreground text-sm">{t("skills.loadingSkills")}</p>
  }
  if (skills.length === 0) {
    return (
      <p className="text-muted-foreground text-sm">
        {t("skills.noSkills")}
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
                    className="bg-state-clean-50 text-state-clean-600 border-state-clean-500/30 inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium"
                    title={t("skills.boundTooltip")}
                  >
                    <Link2 className="size-3" aria-hidden />
                    {t("skills.bound")}
                  </span>
                ) : null}
              </button>
              <div className="text-muted-foreground text-xs">
                {t("skills.versionCount", { count: s.version_count, kind: s.latest_kind })}
              </div>
            </div>
            {open ? <SkillDetailPanel name={s.name} onChanged={onChanged} /> : null}
          </li>
        )
      })}
    </ul>
  )
}
