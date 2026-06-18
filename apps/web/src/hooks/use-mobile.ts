import * as React from "react"

// useIsMobile reports whether the viewport is below the md breakpoint
// (768px), so the layout (t5) can swap the fixed Sidebar for a Sheet drawer.
//
// Implemented with useSyncExternalStore: it subscribes to the matchMedia
// query and reads the current match on each render, which keeps the value in
// sync without a setState-in-effect (the lint rule the simpler version trips).
const MOBILE_BREAKPOINT = 768
const QUERY = `(max-width: ${MOBILE_BREAKPOINT - 1}px)`

function subscribe(callback: () => void): () => void {
  const mql = window.matchMedia(QUERY)
  mql.addEventListener("change", callback)
  return () => mql.removeEventListener("change", callback)
}

function getSnapshot(): boolean {
  return window.matchMedia(QUERY).matches
}

// getServerSnapshot: there is no SSR here, but useSyncExternalStore requires
// it; default to "not mobile" so the desktop layout renders first.
function getServerSnapshot(): boolean {
  return false
}

export function useIsMobile(): boolean {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}
