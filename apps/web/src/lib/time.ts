// Deterministic relative-time formatting shared across cards that poll a
// rendered-at timestamp (the react-hooks/purity rule forbids calling
// Date.now() during render, so callers capture renderedAt once and pass it
// in). Several Phase 6–8 components each grew a private copy of this ladder;
// they now share this one. The empty label varies per caller (从未 / 未知时间
// / 暂无), so it is a parameter.
export function formatRelativeTime(
  ts: number | undefined,
  renderedAt: number,
  emptyLabel: string,
): string {
  if (!ts) return emptyLabel
  const delta = Math.max(0, renderedAt - ts)
  if (delta < 5_000) return "刚刚"
  if (delta < 60_000) return `${Math.floor(delta / 1000)} 秒前`
  if (delta < 60 * 60_000) return `${Math.floor(delta / 60_000)} 分钟前`
  if (delta < 24 * 60 * 60_000) return `${Math.floor(delta / 3_600_000)} 小时前`
  return new Date(ts).toLocaleString("zh-CN")
}
