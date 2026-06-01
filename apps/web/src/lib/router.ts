// Trivial hash-based router so we don't pull in react-router for two
// routes. Locations: #/login | #/setup | #/dashboard. The empty hash
// behaves as #/dashboard so the bare URL lands a logged-in user on
// the dashboard.

import { useEffect, useState } from "react"

export type Route = "login" | "setup" | "dashboard"

function readRoute(): Route {
  const raw = window.location.hash.replace(/^#\/?/, "")
  switch (raw) {
    case "login":
      return "login"
    case "setup":
      return "setup"
    case "":
    case "dashboard":
      return "dashboard"
    default:
      return "dashboard"
  }
}

export function navigate(r: Route): void {
  window.location.hash = `#/${r}`
}

export function useRoute(): Route {
  const [route, setRoute] = useState<Route>(() => readRoute())
  useEffect(() => {
    const handler = () => setRoute(readRoute())
    window.addEventListener("hashchange", handler)
    return () => window.removeEventListener("hashchange", handler)
  }, [])
  return route
}
