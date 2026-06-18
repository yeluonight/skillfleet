import {
  LayoutDashboard,
  MonitorSmartphone,
  Boxes,
  Link2,
  RefreshCw,
  Rocket,
  ScrollText,
  Settings,
  type LucideIcon,
} from "lucide-react"
import type { ParseKeys } from "i18next"

// nav.ts is the single source of truth for the app's primary navigation
// (§13.8.3): the sidebar (t5), the mobile bottom tab bar, and the dashboard
// metric-card click-throughs (t9) all read from here so a route's path,
// label key, and icon are defined once.
//
// labelKey is a typed i18n key (ParseKeys) so a typo or a key not present in
// the dictionary is a compile error, matching t()'s own strictness.

export type NavItem = {
  path: string
  labelKey: ParseKeys
  icon: LucideIcon
}

// PRIMARY_NAV are the workflow pages. SECONDARY_NAV (Settings) sits below a
// separator in the sidebar. The order is the operator's typical top-to-bottom
// flow: overview → fleet → catalog → provenance → change → ship → audit.
export const PRIMARY_NAV: NavItem[] = [
  { path: "/dashboard", labelKey: "nav.dashboard", icon: LayoutDashboard },
  { path: "/devices", labelKey: "nav.devices", icon: MonitorSmartphone },
  { path: "/skills", labelKey: "nav.skills", icon: Boxes },
  { path: "/sources", labelKey: "nav.sources", icon: Link2 },
  { path: "/updates", labelKey: "nav.updates", icon: RefreshCw },
  { path: "/deploys", labelKey: "nav.deploys", icon: Rocket },
  { path: "/audit", labelKey: "nav.audit", icon: ScrollText },
]

export const SECONDARY_NAV: NavItem[] = [
  { path: "/settings", labelKey: "nav.settings", icon: Settings },
]

// MOBILE_TAB_NAV is the condensed bottom bar shown below the md breakpoint —
// the five highest-traffic destinations (the full set lives in the drawer).
export const MOBILE_TAB_NAV: NavItem[] = [
  { path: "/dashboard", labelKey: "nav.dashboard", icon: LayoutDashboard },
  { path: "/devices", labelKey: "nav.devices", icon: MonitorSmartphone },
  { path: "/skills", labelKey: "nav.skills", icon: Boxes },
  { path: "/updates", labelKey: "nav.updates", icon: RefreshCw },
  { path: "/audit", labelKey: "nav.audit", icon: ScrollText },
]
