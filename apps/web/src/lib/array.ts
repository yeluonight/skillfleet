// Small array helpers shared across components.

// groupByKey groups items into ordered buckets by a string key. Insertion
// order of first appearance is preserved (a single pass over a pre-sorted
// list yields stable, contiguous groups), so callers that sort upstream get
// deterministic output. Generalises the per-tool grouping the inventory
// matrix used to hardcode, so the same helper drives both the by-tool and
// by-path views (mgmt-refactor track E).
export function groupByKey<T>(items: T[], keyFn: (item: T) => string): { key: string; items: T[] }[] {
  const groups: { key: string; items: T[] }[] = []
  const index = new Map<string, { key: string; items: T[] }>()
  for (const item of items) {
    const key = keyFn(item)
    let g = index.get(key)
    if (!g) {
      g = { key, items: [] }
      index.set(key, g)
      groups.push(g)
    }
    g.items.push(item)
  }
  return groups
}
