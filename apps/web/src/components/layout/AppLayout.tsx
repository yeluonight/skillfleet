import { useTranslation } from "react-i18next"
import { NavLink, Outlet, useNavigate } from "react-router-dom"
import { LogOut } from "lucide-react"

import { api } from "@/lib/api"
import type { User } from "@/lib/api"
import { cn } from "@/lib/utils"
import { MOBILE_TAB_NAV } from "@/lib/nav"
import {
  SidebarProvider,
  SidebarInset,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { Toaster } from "@/components/ui/sonner"
import { AppSidebar } from "@/components/layout/AppSidebar"

// AppLayout is the authenticated shell (§13.8.3 / §13.8.12): a 240px sidebar
// (a Sheet drawer below md), a top bar with the sidebar toggle + user menu,
// and an <Outlet> for the active page. A bottom tab bar appears on mobile for
// the highest-traffic destinations. Login/Setup render OUTSIDE this shell.
export function AppLayout({ user, onSignedOut }: { user: User; onSignedOut: () => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()

  async function handleLogout() {
    try {
      await api.logout()
    } catch {
      // Even if the server rejects, force the client into a clean state —
      // the cookies are likely already invalid.
    }
    onSignedOut()
    navigate("/login")
  }

  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset>
        <header className="flex h-14 shrink-0 items-center gap-2 border-b px-4">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="mr-2 h-4" />
          <div className="ml-auto flex items-center gap-2">
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="sm" className="gap-2">
                  <span className="text-muted-foreground hidden sm:inline">
                    {t("auth.signedInAs", { name: user.username })}
                  </span>
                  <span className="sm:hidden font-medium">{user.username}</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuLabel>{user.username}</DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={handleLogout}>
                  <LogOut aria-hidden />
                  {t("auth.signOut")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>

        {/* pb-16 on mobile leaves room for the fixed bottom tab bar. */}
        <main className="flex-1 overflow-auto p-4 pb-20 md:p-6 md:pb-6">
          <Outlet />
        </main>

        <MobileTabBar />
      </SidebarInset>
      <Toaster />
    </SidebarProvider>
  )
}

// MobileTabBar is the fixed bottom navigation shown below md. It mirrors the
// five highest-traffic sidebar entries; the full set stays in the drawer
// (open via the header's SidebarTrigger).
function MobileTabBar() {
  const { t } = useTranslation()
  return (
    <nav className="bg-background fixed inset-x-0 bottom-0 z-30 flex h-16 items-stretch border-t md:hidden">
      {MOBILE_TAB_NAV.map((item) => {
        const Icon = item.icon
        return (
          <NavLink
            key={item.path}
            to={item.path}
            className={({ isActive }) =>
              cn(
                "flex flex-1 flex-col items-center justify-center gap-1 text-xs",
                isActive ? "text-primary" : "text-muted-foreground"
              )
            }
          >
            <Icon className="size-5" aria-hidden />
            <span className="truncate">{t(item.labelKey)}</span>
          </NavLink>
        )
      })}
    </nav>
  )
}
