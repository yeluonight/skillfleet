import { AlertTriangle, CheckCircle2, XCircle } from "lucide-react"
import type { ValidationIssue } from "@/lib/api"

/** Props for the ValidationPanel component. */
export interface ValidationPanelProps {
  /** null = never validated; [] = passed; non-empty = findings */
  issues: ValidationIssue[] | null
  /** Called when the user clicks a jumpable issue. */
  onJump: (path: string, line?: number, col?: number) => void
}

/**
 * Renders draft validation results in the right pane of the draft editor.
 *
 * Three states:
 * - null: grey prompt "Run validate to see results."
 * - empty array: green check "No issues found."
 * - non-empty: severity-grouped (errors first, then warnings), each issue
 *   clickable to jump to the file+line+col in Monaco.
 */
export function ValidationPanel({ issues, onJump }: ValidationPanelProps) {
  if (issues === null) {
    return (
      <p className="text-muted-foreground text-xs">
        Run validate to see results.
      </p>
    )
  }

  if (issues.length === 0) {
    return (
      <p className="flex items-center gap-1.5 text-xs text-emerald-600">
        <CheckCircle2 className="size-3.5" aria-hidden />
        No issues found.
      </p>
    )
  }

  const errors = issues.filter((i) => i.severity === "error")
  const warnings = issues.filter((i) => i.severity === "warning")

  return (
    <div className="space-y-3 text-xs">
      {errors.length > 0 && (
        <SeverityGroup
          label={`Errors (${errors.length})`}
          labelColor="text-red-600"
          issues={errors}
          onJump={onJump}
        />
      )}
      {warnings.length > 0 && (
        <SeverityGroup
          label={`Warnings (${warnings.length})`}
          labelColor="text-amber-600"
          issues={warnings}
          onJump={onJump}
        />
      )}
    </div>
  )
}

function SeverityGroup({
  label,
  labelColor,
  issues,
  onJump,
}: {
  label: string
  labelColor: string
  issues: ValidationIssue[]
  onJump: (path: string, line?: number, col?: number) => void
}) {
  return (
    <div className="space-y-1">
      <p className={`font-semibold ${labelColor}`}>{label}</p>
      <ul className="space-y-0.5">
        {issues.map((i, idx) => {
          const hasPath = !!i.path
          const lineSeg = i.line && i.line > 0 ? `:${i.line}` : ""
          const colSeg = i.col && i.col > 0 ? `:${i.col}` : ""
          const location = hasPath ? `${i.path}${lineSeg}${colSeg} · ${i.code}` : i.code
          const isError = i.severity === "error"
          const Icon = isError ? XCircle : AlertTriangle
          const iconColor = isError ? "text-red-600" : "text-amber-600"

          const body = (
            <>
              <div className="flex items-start gap-1.5">
                <Icon className={`${iconColor} size-3.5 mt-px shrink-0`} aria-hidden />
                <span>{i.message}</span>
              </div>
              <div className="text-muted-foreground text-[11px] font-mono">
                {location}
              </div>
            </>
          )

          if (hasPath) {
            return (
              <li key={idx}>
                <button
                  className="w-full text-left hover:bg-accent rounded px-2 py-1"
                  onClick={() => onJump(i.path!, i.line, i.col)}
                >
                  {body}
                </button>
              </li>
            )
          }

          return (
            <li key={idx} className="cursor-default rounded px-2 py-1">
              {body}
            </li>
          )
        })}
      </ul>
    </div>
  )
}