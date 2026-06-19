import { useTranslation } from "react-i18next"
import { NavLink } from "react-router-dom"
import { Rocket } from "lucide-react"

import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarSeparator,
} from "@/components/ui/sidebar"
import { PRIMARY_NAV, SECONDARY_NAV, type NavItem } from "@/lib/nav"

// AppSidebar is the desktop left navigation (§13.8.3). It renders the primary
// workflow links, a separator, then Settings, plus the brand header. NavLink
// drives the active state so the current page highlights without manual route
// comparison. On mobile the same content renders inside a Sheet (handled by
// the Sidebar component's collapsible="mobile" path).
export function AppSidebar() {
  const { t } = useTranslation()

  return (
    <Sidebar>
      <SidebarHeader>
        <div className="flex items-center gap-2 px-2 py-1">
          <Rocket className="text-primary size-5 shrink-0" aria-hidden />
          <span className="text-base font-semibold tracking-tight">
            {t("auth.appName")}
          </span>
        </div>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              {PRIMARY_NAV.map((item) => (
                <NavItemButton key={item.path} item={item} label={t(item.labelKey)} />
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        <SidebarSeparator />
        <SidebarGroup>
          <SidebarGroupContent>
            <SidebarMenu>
              {SECONDARY_NAV.map((item) => (
                <NavItemButton key={item.path} item={item} label={t(item.labelKey)} />
              ))}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter />
    </Sidebar>
  )
}

// NavItemButton wires a nav entry to a NavLink, asChild so the SidebarMenuButton
// styling applies to the anchor. NavLink's render-prop exposes isActive, which
// drives the button's data-active highlight.
function NavItemButton({ item, label }: { item: NavItem; label: string }) {
  const Icon = item.icon
  return (
    <SidebarMenuItem>
      <NavLink to={item.path}>
        {({ isActive }) => (
          <SidebarMenuButton asChild isActive={isActive} tooltip={label}>
            <span>
              <Icon aria-hidden />
              <span>{label}</span>
            </span>
          </SidebarMenuButton>
        )}
      </NavLink>
    </SidebarMenuItem>
  )
}
