import { useEffect, useState } from "react"
import { Toaster as Sonner, type ToasterProps } from "sonner"

// Toaster wraps sonner's <Toaster/>. The app manages dark mode via a `.dark`
// class on <html> (no next-themes), so we observe that class and pass the
// resolved theme to sonner. CSS variables map sonner's surfaces onto our
// design tokens so toasts match the rest of the UI.
function useHtmlTheme(): "light" | "dark" {
  const [theme, setTheme] = useState<"light" | "dark">(() =>
    typeof document !== "undefined" &&
    document.documentElement.classList.contains("dark")
      ? "dark"
      : "light"
  )
  useEffect(() => {
    const el = document.documentElement
    const obs = new MutationObserver(() => {
      setTheme(el.classList.contains("dark") ? "dark" : "light")
    })
    obs.observe(el, { attributes: true, attributeFilter: ["class"] })
    return () => obs.disconnect()
  }, [])
  return theme
}

function Toaster(props: ToasterProps) {
  const theme = useHtmlTheme()
  return (
    <Sonner
      theme={theme}
      className="toaster group"
      position="bottom-right"
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
        } as React.CSSProperties
      }
      {...props}
    />
  )
}

export { Toaster }
