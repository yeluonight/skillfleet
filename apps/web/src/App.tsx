import { useCallback, useEffect, useState } from "react"
import { Loader2 } from "lucide-react"

import { api, ApiError } from "@/lib/api"
import type { User } from "@/lib/api"
import { navigate, useRoute } from "@/lib/router"
import { LoginPage } from "@/pages/LoginPage"
import { SetupPage } from "@/pages/SetupPage"
import { DashboardPage } from "@/pages/DashboardPage"

// AuthState is the central client-side state machine. We resolve it on
// mount by trying /api/me; on 401 we fall back to /api/status to decide
// between login and setup. Components that mutate auth (login, setup,
// logout) call refresh() so the same code path picks the new state.
type AuthState =
  | { kind: "loading" }
  | { kind: "needs_setup" }
  | { kind: "signed_out" }
  | { kind: "signed_in"; user: User }

export default function App() {
  const route = useRoute()
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
        // Network / 5xx: stay on a sane page; treat as signed out so
        // the user can retry from the login screen.
        setState({ kind: "signed_out" })
      }
    }
  }, [])

  useEffect(() => {
    // Resolve initial auth state once on mount. The fetch chain is
    // self-contained so we can detach with `cancelled` to avoid
    // setting state after unmount during React StrictMode's double
    // invocation. The actual refresh() helper is exposed to children
    // so login/logout can re-resolve without prop-drilling fetchers.
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

  // Route guard. When the URL hash conflicts with the resolved auth
  // state we redirect rather than render a half-valid page.
  useEffect(() => {
    if (state.kind === "loading") return
    if (state.kind === "needs_setup" && route !== "setup") navigate("setup")
    else if (state.kind === "signed_out" && route !== "login") navigate("login")
    else if (state.kind === "signed_in" && (route === "login" || route === "setup")) navigate("dashboard")
  }, [state, route])

  if (state.kind === "loading") return <SplashLoading />
  if (state.kind === "needs_setup") return <SetupPage onSetupComplete={refresh} />
  if (state.kind === "signed_out") return <LoginPage onSignedIn={refresh} />
  return <DashboardPage user={state.user} onSignedOut={refresh} />
}

function SplashLoading() {
  return (
    <main className="bg-background flex min-h-svh items-center justify-center">
      <Loader2 className="text-muted-foreground size-6 animate-spin" aria-hidden />
      <span className="sr-only">Loading…</span>
    </main>
  )
}
