/**
 * Pure functions for JSON formatting and minification.
 * Used by the Monaco editor toolbar to pretty-print or compress JSON content.
 *
 * All functions are non-throwing — errors are returned as structured results.
 */

export type JsonToolResult =
  | { ok: true; text: string }
  | { ok: false; error: string }

/**
 * Extract line and column from an offset in the source string (1-based).
 * Used to enrich SyntaxError messages with human-readable position info.
 */
function offsetToLineCol(source: string, offset: number): { line: number; col: number } | null {
  if (offset < 0 || offset > source.length) return null

  let line = 1
  let col = 1
  for (let i = 0; i < offset; i++) {
    if (source[i] === '\n') {
      line++
      col = 1
    } else {
      col++
    }
  }
  return { line, col }
}

/**
 * Try to extract a human-readable error message from a JSON SyntaxError.
 * V8 engines include position info like "... in JSON at position 42".
 */
function buildJsonError(e: SyntaxError, source: string): string {
  const raw = e.message
  // Match V8-style "... in JSON at position N" — the N may include line/col variants too
  const match = raw.match(/position\s+(\d+)/i)
  if (match) {
    const pos = parseInt(match[1], 10)
    const lc = offsetToLineCol(source, pos)
    if (lc) {
      return `Invalid JSON: ${raw} (line ${lc.line}, col ${lc.col})`
    }
  }
  return `Invalid JSON: ${raw}`
}

/**
 * Format (pretty-print) a JSON string with 2-space indentation.
 * Returns { ok: true, text } on success, or { ok: false, error } on failure.
 */
export function formatJson(input: string): JsonToolResult {
  if (input.trim() === '') {
    return { ok: false, error: 'Empty input' }
  }
  try {
    const parsed = JSON.parse(input)
    return { ok: true, text: JSON.stringify(parsed, null, 2) }
  } catch (e) {
    if (e instanceof SyntaxError) {
      return { ok: false, error: buildJsonError(e, input) }
    }
    return { ok: false, error: `Invalid JSON: ${String(e)}` }
  }
}

/**
 * Compress (minify) a JSON string into a single line with no extra whitespace.
 * Returns { ok: true, text } on success, or { ok: false, error } on failure.
 */
export function minifyJson(input: string): JsonToolResult {
  if (input.trim() === '') {
    return { ok: false, error: 'Empty input' }
  }
  try {
    const parsed = JSON.parse(input)
    return { ok: true, text: JSON.stringify(parsed) }
  } catch (e) {
    if (e instanceof SyntaxError) {
      return { ok: false, error: buildJsonError(e, input) }
    }
    return { ok: false, error: `Invalid JSON: ${String(e)}` }
  }
}