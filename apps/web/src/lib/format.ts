// Byte-size formatting shared across file-list views (diff viewers, the bind
// wizard). Phase 5–7 each grew a private copy; they now share this one.
export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${trimFixed(bytes / 1024)} KB`
  return `${trimFixed(bytes / (1024 * 1024))} MB`
}

// trimFixed renders one decimal place but drops a trailing ".0".
export function trimFixed(value: number): string {
  return value.toFixed(1).replace(/\.0$/, "")
}
