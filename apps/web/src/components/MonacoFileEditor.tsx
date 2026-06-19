import { useCallback, useEffect, useState } from "react"
import { Editor } from "@monaco-editor/react"
import type { editor } from "monaco-editor"
import { useTranslation } from "react-i18next"

import { languageForPath } from "@/lib/language-map"

export interface MonacoFileEditorProps {
  /** File path used to derive the editor language. */
  path: string
  /** Current file content. */
  value: string
  /** Called when the user edits the content. */
  onChange: (next: string) => void
  /** When true the editor is read-only. Defaults to false. */
  readOnly?: boolean
  /** Editor height (CSS value). Defaults to "16rem". */
  height?: string | number
  /**
   * Called once the editor instance is mounted. The parent can keep the
   * instance to drive imperative actions (e.g. reveal a line/column when
   * the user clicks a validation issue). Cleared again on unmount.
   */
  onReady?: (editor: editor.IStandaloneCodeEditor | null) => void
}

/**
 * A Monaco-powered file editor that follows the global .dark class for
 * theme. Language is derived from the file path via languageForPath.
 *
 * Theme is locked to the app-level .dark class on <html>; the editor
 * does not offer an independent theme switcher (per §13.8.13 item 2).
 */
export function MonacoFileEditor({
  path,
  value,
  onChange,
  readOnly = false,
  height = "16rem",
  onReady,
}: MonacoFileEditorProps) {
  const { t } = useTranslation()
  const [isDark, setIsDark] = useState<boolean>(
    () => document.documentElement.classList.contains("dark"),
  )

  // Watch <html> class changes so the editor theme follows the app's
  // dark/light toggle automatically.
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

  const handleChange = useCallback(
    (next: string | undefined) => {
      onChange(next ?? "")
    },
    [onChange],
  )

  const handleMount = useCallback(
    (instance: editor.IStandaloneCodeEditor) => {
      onReady?.(instance)
    },
    [onReady],
  )

  // Release the instance held by the parent when this editor unmounts,
  // so a stale instance is never driven after the file view is gone.
  useEffect(() => {
    return () => onReady?.(null)
  }, [onReady])

  return (
    <Editor
      path={path}
      language={languageForPath(path)}
      theme={isDark ? "vs-dark" : "vs"}
      value={value}
      onChange={handleChange}
      onMount={handleMount}
      height={height}
      loading={
        <span className="text-muted-foreground text-xs">{t("skills.loadingEditor")}</span>
      }
      options={{
        lineNumbers: "on",
        wordWrap: "on",
        minimap: { enabled: false },
        fontSize: 13,
        scrollBeyondLastLine: false,
        automaticLayout: true,
        fontFamily:
          "JetBrains Mono, Fira Code, ui-monospace, monospace",
        renderWhitespace: "selection",
        tabSize: 2,
        readOnly,
      }}
    />
  )
}