import { cn } from "@/lib/utils"
import { useCallback, useEffect, useRef, useState } from "react"

export interface SplitPaneProps {
  /** Left panel content (file tree). */
  left: React.ReactNode
  /** Center panel content (editor). */
  center: React.ReactNode
  /** Right panel content (preview/validation). */
  right: React.ReactNode
  /** localStorage key for persisting split ratios. Defaults to "sf.editor.split". */
  storageKey?: string
  className?: string
}

const DEFAULT_PCTS: [number, number, number] = [20, 50, 30]
const MIN_PCT = 10

/**
 * A three-panel horizontal resizable split pane with two draggable gutters.
 *
 * - Left gutter adjusts the left and center panels (right is unchanged).
 * - Right gutter adjusts the center and right panels (left is unchanged).
 * - Each panel is clamped to MIN_PCT (10%) during drag.
 * - Split ratios are persisted to localStorage on drag end.
 */
export function SplitPane({
  left,
  center,
  right,
  storageKey = "sf.editor.split",
  className,
}: SplitPaneProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const latestPctsRef = useRef(DEFAULT_PCTS)
  const dragState = useRef<{
    gutter: "left" | "right"
    startX: number
    startPcts: [number, number, number]
  } | null>(null)

  const [pcts, setPcts] = useState<[number, number, number]>(() => {
    try {
      const raw = localStorage.getItem(storageKey)
      if (raw) {
        const parsed = JSON.parse(raw) as unknown
        if (
          Array.isArray(parsed) &&
          parsed.length === 3 &&
          parsed.every((n: unknown) => typeof n === "number" && n >= 0) &&
          Math.abs(parsed[0] + parsed[1] + parsed[2] - 100) < 1
        ) {
          return parsed as [number, number, number]
        }
      }
    } catch {
      // ignore parse failures, fall through to defaults
    }
    return DEFAULT_PCTS
  })

  const persist = useCallback(
    (value: [number, number, number]) => {
      try {
        localStorage.setItem(storageKey, JSON.stringify(value))
      } catch {
        // ignore storage errors (quota exceeded, private browsing, etc.)
      }
    },
    [storageKey],
  )

  const handleMouseDown = useCallback(
    (gutter: "left" | "right") => (e: React.MouseEvent) => {
      e.preventDefault()

      const container = containerRef.current
      if (!container) return

      dragState.current = {
        gutter,
        startX: e.clientX,
        startPcts: [...latestPctsRef.current] as [number, number, number],
      }

      document.body.style.userSelect = "none"

      const handleMouseMove = (me: MouseEvent) => {
        const state = dragState.current
        if (!state) return

        const rect = container.getBoundingClientRect()
        const dx = me.clientX - state.startX
        const dpct = (dx / rect.width) * 100

        let next: [number, number, number]

        if (state.gutter === "left") {
          // Adjust left and center; right stays put.
          let l = state.startPcts[0] + dpct
          let c = state.startPcts[1] - dpct

          if (l < MIN_PCT) {
            c -= MIN_PCT - l
            l = MIN_PCT
          }
          if (c < MIN_PCT) {
            l -= MIN_PCT - c
            c = MIN_PCT
          }

          next = [l, c, state.startPcts[2]]
        } else {
          // Adjust center and right; left stays put.
          let c = state.startPcts[1] + dpct
          let r = state.startPcts[2] - dpct

          if (c < MIN_PCT) {
            r -= MIN_PCT - c
            c = MIN_PCT
          }
          if (r < MIN_PCT) {
            c -= MIN_PCT - r
            r = MIN_PCT
          }

          next = [state.startPcts[0], c, r]
        }

        latestPctsRef.current = next
        setPcts(next)
      }

      const handleMouseUp = () => {
        document.removeEventListener("mousemove", handleMouseMove)
        document.removeEventListener("mouseup", handleMouseUp)
        document.body.style.userSelect = ""
        dragState.current = null
        persist(latestPctsRef.current)
      }

      document.addEventListener("mousemove", handleMouseMove)
      document.addEventListener("mouseup", handleMouseUp)
    },
    [persist],
  )

  // Cleanup on unmount if drag is still in progress.
  useEffect(() => {
    return () => {
      document.body.style.userSelect = ""
      dragState.current = null
    }
  }, [])

  return (
    <div
      ref={containerRef}
      className={cn("flex h-full w-full", className)}
    >
      {/* Left panel */}
      <div
        className="overflow-auto min-w-0"
        style={{ width: `${pcts[0]}%` }}
      >
        {left}
      </div>

      {/* Left gutter */}
      <div
        role="separator"
        aria-orientation="vertical"
        className="shrink-0 w-1.5 cursor-col-resize select-none bg-border hover:bg-primary/40 transition-colors"
        onMouseDown={handleMouseDown("left")}
      />

      {/* Center panel */}
      <div
        className="overflow-auto min-w-0"
        style={{ width: `${pcts[1]}%` }}
      >
        {center}
      </div>

      {/* Right gutter */}
      <div
        role="separator"
        aria-orientation="vertical"
        className="shrink-0 w-1.5 cursor-col-resize select-none bg-border hover:bg-primary/40 transition-colors"
        onMouseDown={handleMouseDown("right")}
      />

      {/* Right panel */}
      <div
        className="overflow-auto min-w-0"
        style={{ width: `${pcts[2]}%` }}
      >
        {right}
      </div>
    </div>
  )
}