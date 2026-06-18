import { useCallback, useEffect, useRef, useState } from "react"

import { apiErrorMessage } from "@/lib/api"

export type UseApiResource<T> = {
  /** Latest data; null until the first successful load. */
  data: T | null
  /** True while a non-silent fetch is in flight. */
  loading: boolean
  /** Normalised error message from the last non-silent failed fetch. */
  error: string | null
  /** Re-run the fetcher. Resolves when the fetch settles. */
  refresh: () => Promise<void>
  /** Imperatively replace the data (e.g. optimistic update). */
  setData: React.Dispatch<React.SetStateAction<T | null>>
  /** Imperatively set/clear the error. */
  setError: React.Dispatch<React.SetStateAction<string | null>>
}

type Options = {
  /** Re-run the initial load whenever one of these changes (like an effect dep array). Defaults to []. */
  deps?: React.DependencyList
  /** When set, also poll the fetcher on this interval (ms). Poll fetches are silent. */
  pollMs?: number
  /** Fallback message when a thrown value isn't an ApiError. */
  errorFallback?: string
}

// useApiResource folds the recurring "load on mount (+ optional poll),
// track loading/error, expose a manual refresh" boilerplate into one hook.
// It replaces the hand-rolled `let cancelled = false` IIFE-in-effect plus a
// near-duplicate standalone refresh() that ~7 components each carried.
//
// The mount/poll fetches are silent (they don't toggle `loading` or clear a
// visible error mid-poll, so a transient blip doesn't flash the operator);
// an explicit refresh() surfaces loading + errors. A cancel ref makes any
// in-flight fetch no-op after the relevant deps change or the component
// unmounts, so late setStates never land.
export function useApiResource<T>(
  fetcher: (signal: { cancelled: boolean }) => Promise<T>,
  opts: Options = {},
): UseApiResource<T> {
  const { deps = [], pollMs, errorFallback = "加载失败。" } = opts
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Keep the latest fetcher/fallback in refs so refresh()'s identity stays
  // stable across renders (callers pass an inline fetcher each render).
  // Synced in an effect, not at render time, per react-hooks/refs.
  const fetcherRef = useRef(fetcher)
  const fallbackRef = useRef(errorFallback)
  useEffect(() => {
    fetcherRef.current = fetcher
    fallbackRef.current = errorFallback
  })

  // run executes one fetch. silent fetches (mount/poll) don't toggle loading
  // or set errors; explicit ones do. The shared `token` lets a stale fetch
  // detect it was superseded and skip its setStates.
  const run = useCallback(async (token: { cancelled: boolean }, silent: boolean) => {
    if (!silent) {
      setLoading(true)
      setError(null)
    }
    try {
      const result = await fetcherRef.current(token)
      if (token.cancelled) return
      setData(result)
    } catch (err) {
      if (silent || token.cancelled) return
      setError(apiErrorMessage(err, fallbackRef.current))
    } finally {
      if (!silent && !token.cancelled) setLoading(false)
    }
  }, [])

  // A ref to the live token so the stable refresh() callback always targets
  // the current effect's lifecycle (a refresh after a dep change shouldn't
  // be cancelled by the previous effect's cleanup).
  const tokenRef = useRef({ cancelled: false })

  const refresh = useCallback(() => run(tokenRef.current, false), [run])

  useEffect(() => {
    const token = { cancelled: false }
    tokenRef.current = token
    // Initial load is silent: components render their own "Loading…" from
    // data===null, matching the pre-refactor mount behaviour.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void run(token, true)
    let timer: ReturnType<typeof setInterval> | undefined
    if (pollMs) {
      timer = setInterval(() => void run(token, true), pollMs)
    }
    return () => {
      token.cancelled = true
      if (timer) clearInterval(timer)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  return { data, loading, error, refresh, setData, setError }
}
