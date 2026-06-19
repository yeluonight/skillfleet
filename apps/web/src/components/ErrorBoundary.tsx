import { Component, type ErrorInfo, type ReactNode } from "react"
import { useLocation } from "react-router-dom"
import { useTranslation } from "react-i18next"
import { AlertTriangle } from "lucide-react"

import { Button } from "@/components/ui/button"

// ErrorBoundary catches render-time exceptions in its subtree and shows a
// localized fallback instead of letting the whole React tree unmount (which
// would blank the page — there was no boundary before, so a single bad enum
// branch could white-screen the app). React error boundaries must be class
// components (componentDidCatch / getDerivedStateFromError have no hook
// equivalent), so the localized chrome is injected via props from the
// RouteErrorBoundary wrapper below.
//
// resetKey: when it changes (we pass the route pathname) the boundary clears
// its error, so navigating away from a broken page recovers without a reload.
type Props = {
  children: ReactNode
  resetKey: string
  title: string
  retryLabel: string
  onReset: () => void
}

type State = { error: Error | null }

class ErrorBoundaryInner extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidUpdate(prev: Props) {
    // Clear the error when the route changes so a different page renders fresh.
    if (this.state.error && prev.resetKey !== this.props.resetKey) {
      this.setState({ error: null })
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Surface to the console for diagnosis; no telemetry sink in this app.
    console.error("ErrorBoundary caught:", error, info.componentStack)
  }

  render() {
    if (this.state.error) {
      return (
        <div
          role="alert"
          className="border-state-danger-500/30 bg-state-danger-50 text-state-danger-600 flex flex-col items-start gap-3 rounded-md border p-4"
        >
          <div className="flex items-center gap-2 text-sm font-medium">
            <AlertTriangle className="size-4 shrink-0" aria-hidden />
            {this.props.title}
          </div>
          <p className="text-state-danger-600/80 font-mono text-xs break-all">
            {this.state.error.message}
          </p>
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              this.setState({ error: null })
              this.props.onReset()
            }}
          >
            {this.props.retryLabel}
          </Button>
        </div>
      )
    }
    return this.props.children
  }
}

// RouteErrorBoundary wires the class boundary to i18n + the current route, so
// any page rendered under the layout degrades to a local panel (nav/chrome
// stay) instead of white-screening, and recovers on navigation.
export function RouteErrorBoundary({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const location = useLocation()
  return (
    <ErrorBoundaryInner
      resetKey={location.pathname}
      title={t("common.sectionError")}
      retryLabel={t("common.retry")}
      onReset={() => {}}
    >
      {children}
    </ErrorBoundaryInner>
  )
}
