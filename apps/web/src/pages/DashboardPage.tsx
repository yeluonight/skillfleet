import { LogOut, Rocket } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { DevicesCard } from "@/components/DevicesCard"
import { DevicesList } from "@/components/DevicesList"
import { SkillsCard } from "@/components/SkillsCard"
import { UpdatesCard } from "@/components/UpdatesCard"

import { api } from "@/lib/api"
import type { UpdatesResponse, User } from "@/lib/api"
import { useApiResource } from "@/hooks/useApiResource"
import { navigate } from "@/lib/router"

export function DashboardPage({ user, onSignedOut }: { user: User; onSignedOut: () => void }) {
  // Updates Page data (§13.7) lives at the dashboard level so its summary
  // cards sit up top. All api/CSRF/error handling is here; UpdatesCard is
  // pure. The initial load is silent (cards render their own empty state);
  // an explicit refresh surfaces the busy spinner + errors.
  const {
    data: updates,
    loading: updatesBusy,
    error: updatesError,
    refresh: loadUpdates,
  } = useApiResource<UpdatesResponse>(() => api.listUpdates(), {
    errorFallback: "Failed to load updates.",
  })

  // onSelectSkill scrolls to the registry section so the operator can expand
  // the flagged skill and act on its source tab. (A dedicated route lands
  // with the Phase 7 diff/apply work.)
  function focusSkills() {
    document.getElementById("sf-skills-section")?.scrollIntoView({ behavior: "smooth", block: "start" })
  }

  async function handleLogout() {
    try {
      await api.logout()
    } catch {
      // Even if the server rejects, force the client into a clean
      // state — the cookies are likely already invalid.
    }
    onSignedOut()
    navigate("login")
  }

  const expires = new Date(user.expires_at)

  return (
    <div className="bg-background min-h-svh">
      <header className="border-border flex items-center justify-between border-b px-6 py-4">
        <div className="flex items-center gap-2">
          <Rocket className="text-primary size-5" aria-hidden />
          <span className="text-base font-semibold tracking-tight">SkillFleet</span>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-muted-foreground text-sm">
            Signed in as <span className="text-foreground font-medium">{user.username}</span>
          </span>
          <Button variant="ghost" size="sm" onClick={handleLogout}>
            <LogOut className="size-4" aria-hidden />
            Sign out
          </Button>
        </div>
      </header>

      <main className="mx-auto max-w-4xl px-6 py-10">
        <div className="space-y-6">
          <div>
            <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
            <p className="text-muted-foreground mt-1 text-sm">
              Welcome back. Enrol devices, browse their skill inventory, and manage the central
              skill registry below.
            </p>
          </div>

          <DevicesCard />
          <DevicesList />
          <UpdatesCard
            data={updates}
            error={updatesError}
            busy={updatesBusy}
            onRefresh={loadUpdates}
            onSelectSkill={focusSkills}
          />
          <div id="sf-skills-section">
            <SkillsCard />
          </div>

          <Card>
            <CardHeader>
              <CardTitle className="text-lg">Session</CardTitle>
              <CardDescription>
                Phase 1 baseline. Phase 2 is layering on device enrolment; Phase 4 will light up
                the skill catalog.
              </CardDescription>
            </CardHeader>
            <CardContent className="space-y-2 text-sm">
              <Row label="User ID" value={user.user_id} mono />
              <Row label="Username" value={user.username} />
              <Row label="Session expires" value={expires.toLocaleString()} />
            </CardContent>
          </Card>
        </div>
      </main>
    </div>
  )
}

function Row({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <span className="text-muted-foreground">{label}</span>
      <span className={mono ? "font-mono text-xs" : ""}>{value}</span>
    </div>
  )
}
