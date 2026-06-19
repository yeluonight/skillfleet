import { useCallback, useState } from "react"

import { apiErrorMessage } from "@/lib/api"

export type UseAsyncAction = {
  /** True while the wrapped action is in flight. */
  busy: boolean
  /** Normalised error message from the last failed action, or null. */
  error: string | null
  /** Imperatively set/clear the error (e.g. a pre-flight validation message). */
  setError: React.Dispatch<React.SetStateAction<string | null>>
  /**
   * Run a one-shot write action with busy + error bookkeeping. Clears the
   * error and sets busy before awaiting fn; on throw, stores the normalised
   * message and returns false; on success returns true. busy is always
   * cleared in finally. fn's resolved value is ignored, so an api.* call
   * that returns a payload can be passed directly.
   */
  run: (fn: () => Promise<unknown>, errorFallback: string) => Promise<boolean>
}

// useAsyncAction folds the recurring "setBusy(true) / try await / catch →
// instanceof ApiError message / finally setBusy(false)" wrapper that ~18
// one-shot write handlers each repeated. The boolean return lets callers
// chain post-success work (close dialog, refresh) only when the action
// actually succeeded:
//
//   const act = useAsyncAction()
//   const ok = await act.run(() => api.createSkill(name), "Failed to create skill.")
//   if (ok) { setName(""); await refresh() }
//
// Per-row busy (a `busyId` string rather than one boolean) stays hand-rolled
// — this hook deliberately models the single-flight case, which is the
// common one.
export function useAsyncAction(): UseAsyncAction {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const run = useCallback(async (fn: () => Promise<unknown>, errorFallback: string): Promise<boolean> => {
    setBusy(true)
    setError(null)
    try {
      await fn()
      return true
    } catch (err) {
      setError(apiErrorMessage(err, errorFallback))
      return false
    } finally {
      setBusy(false)
    }
  }, [])

  return { busy, error, setError, run }
}
