import type { TFunction } from "i18next"

// Deterministic relative-time formatting shared across cards that poll a
// rendered-at timestamp (the react-hooks/purity rule forbids calling
// Date.now() during render, so callers capture renderedAt once and pass it
// in). Several Phase 6–8 components each grew a private copy of this ladder;
// they now share this one. The empty label varies per caller (从未 / 未知时间
// / 暂无), so it is a parameter.
//
// i18n-aware (mgmt-refactor track C): the ladder labels come from the
// `time.*` keys via t(), and the absolute fallback uses the runtime locale
// (no hardcoded zh-CN), so the same helper reads correctly in either UI
// language.
export function formatRelativeTime(
  t: TFunction,
  ts: number | undefined,
  renderedAt: number,
  emptyLabel: string,
): string {
  if (!ts) return emptyLabel
  const delta = Math.max(0, renderedAt - ts)
  if (delta < 5_000) return t("time.justNow")
  if (delta < 60_000) return t("time.secondsAgo", { n: Math.floor(delta / 1000) })
  if (delta < 60 * 60_000) return t("time.minutesAgo", { n: Math.floor(delta / 60_000) })
  if (delta < 24 * 60 * 60_000) return t("time.hoursAgo", { n: Math.floor(delta / 3_600_000) })
  return new Date(ts).toLocaleString()
}
