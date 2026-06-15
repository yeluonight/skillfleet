import { useCallback, useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Routes, Route, Navigate } from "react-router-dom"
import { Loader2 } from "lucide-react"

import { api, ApiError } from "@/lib/api"
import type { User } from "@/lib/api"
import { LoginPage } from "@/pages/LoginPage"
import { SetupPage } from "@/pages/SetupPage"
import { DashboardPage } from "@/pages/DashboardPage"
import { DevicesPage } from "@/pages/DevicesPage"
import { SkillsPage } from "@/pages/SkillsPage"
import { SourcesPage } from "@/pages/SourcesPage"
import { UpdatesPage } from "@/pages/UpdatesPage"
import { DeploysPage } from "@/pages/DeploysPage"
import { AuditPage } from "@/pages/AuditPage"
import { SettingsPage } from "@/pages/SettingsPage"
import { AppLayout } from "@/components/layout/AppLayout"

// AuthState is the central client-side state machine. We resolve it on mount
// by trying /api/me; on 401 we fall back to /api/status to decide between
// login and setup. Components that mutate auth (login, setup, logout) call
// refresh() so the same code path picks the new state.
type AuthState =
  | { kind: "loading" }
  | { kind: "needs_setup" }
  | { kind: "signed_out" }
  | { kind: "signed_in"; user: User }

export default function App() {
  const [state, setState] = useState<AuthState>({ kind: "loading" })

  const refresh = useCallback(async () => {
    try {
      const user = await api.me()
      setState({ kind: "signed_in", user })
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        try {
          const status = await api.status()
          setState(status.setup_required ? { kind: "needs_setup" } : { kind: "signed_out" })
        } catch {
          setState({ kind: "signed_out" })
        }
      } else {
        // Network / 5xx: stay on a sane page; treat as signed out so the
        // user can retry from the login screen.
        setState({ kind: "signed_out" })
      }
    }
  }, [])

  useEffect(() => {
    // Resolve initial auth state once on mount. `cancelled` detaches the
    // async chain so we don't setState after unmount during StrictMode's
    // double invocation.
    let cancelled = false
    ;(async () => {
      try {
        const user = await api.me()
        if (!cancelled) setState({ kind: "signed_in", user })
      } catch (err) {
        if (cancelled) return
        if (err instanceof ApiError && err.status === 401) {
          try {
            const status = await api.status()
            if (!cancelled) {
              setState(status.setup_required ? { kind: "needs_setup" } : { kind: "signed_out" })
            }
          } catch {
            if (!cancelled) setState({ kind: "signed_out" })
          }
        } else if (!cancelled) {
          setState({ kind: "signed_out" })
        }
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  if (state.kind === "loading") return <SplashLoading />
  if (state.kind === "needs_setup") return <UnauthedRoutes mode="setup" onResolved={refresh} />
  if (state.kind === "signed_out") return <UnauthedRoutes mode="login" onResolved={refresh} />
  return <AuthedRoutes user={state.user} onSignedOut={refresh} />
}

// UnauthedRoutes renders the login or setup page and pins the URL to the
// matching path, redirecting any other location back to it. This replaces the
// old hash-guard effect: the auth state decides which single page is legal,
// and the catch-all <Navigate> enforces it.
function UnauthedRoutes({ mode, onResolved }: { mode: "login" | "setup"; onResolved: () => void }) {
  const path = `/${mode}`
  return (
    <Routes>
      <Route
        path={path}
        element={
          mode === "setup" ? (
            <SetupPage onSetupComplete={onResolved} />
          ) : (
            <LoginPage onSignedIn={onResolved} />
          )
        }
      />
      <Route path="*" element={<Navigate to={path} replace />} />
    </Routes>
  )
}

// AuthedRoutes mounts the AppLayout shell with the page routes nested under
// its <Outlet>. login/setup redirect to the dashboard (already signed in);
// unknown paths fall back to the dashboard too. Every page now has a real
// body; the placeholder is gone.
function AuthedRoutes({ user, onSignedOut }: { user: User; onSignedOut: () => void }) {
  return (
    <Routes>
      <Route element={<AppLayout user={user} onSignedOut={onSignedOut} />}>
        <Route index element={<Navigate to="/dashboard" replace />} />
        <Route path="/dashboard" element={<DashboardPage />} />
        <Route path="/devices" element={<DevicesPage />} />
        <Route path="/skills" element={<SkillsPage />} />
        <Route path="/sources" element={<SourcesPage />} />
        <Route path="/updates" element={<UpdatesPage />} />
        <Route path="/deploys" element={<DeploysPage />} />
        <Route path="/audit" element={<AuditPage />} />
        <Route path="/settings" element={<SettingsPage onSignedOut={onSignedOut} />} />
        <Route path="/login" element={<Navigate to="/dashboard" replace />} />
        <Route path="/setup" element={<Navigate to="/dashboard" replace />} />
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Route>
    </Routes>
  )
}

function SplashLoading() {
  const { t } = useTranslation()
  return (
    <main className="bg-background flex min-h-svh items-center justify-center">
      <Loader2 className="text-muted-foreground size-6 animate-spin" aria-hidden />
      <span className="sr-only">{t("common.loading")}</span>
    </main>
  )
}
