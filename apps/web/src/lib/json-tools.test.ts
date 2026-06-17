/**
 * Manual assertion tests for json-tools.ts.
 * Run with: node --experimental-strip-types src/lib/json-tools.test.ts
 */

import { formatJson, minifyJson } from './json-tools.ts'

let passed = 0
let failed = 0

function assert(description: string, fn: () => boolean | void): void {
  try {
    const result = fn()
    if (result === false) {
      throw new Error('assertion returned false')
    }
    passed++
    console.log(`  ✓ ${description}`)
  } catch (e) {
    failed++
    console.log(`  ✗ ${description}`)
    console.log(`    ${e instanceof Error ? e.message : String(e)}`)
  }
}

function eq<T>(actual: T, expected: T): boolean {
  if (JSON.stringify(actual) !== JSON.stringify(expected)) {
    throw new Error(`Expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`)
  }
  return true
}

// ---------- formatJson ----------

console.log('\nformatJson')
assert('pretty-prints flat object', () => {
  const r = formatJson('{"a":1}')
  if (!r.ok) throw new Error('expected ok')
  eq(r.text, '{\n  "a": 1\n}')
})

assert('pretty-prints nested object', () => {
  const r = formatJson('{"a":{"b":2}}')
  if (!r.ok) throw new Error('expected ok')
  eq(r.text, '{\n  "a": {\n    "b": 2\n  }\n}')
})

assert('pretty-prints array', () => {
  const r = formatJson('[1,2,3]')
  if (!r.ok) throw new Error('expected ok')
  eq(r.text, '[\n  1,\n  2,\n  3\n]')
})

assert('handles Chinese characters', () => {
  const r = formatJson('{"key":"你好"}')
  if (!r.ok) throw new Error('expected ok')
  eq(r.text, '{\n  "key": "你好"\n}')
})

assert('idempotent: formatting already formatted JSON is stable', () => {
  const once = formatJson('{"a":1}')
  if (!once.ok) throw new Error('first format failed')
  const twice = formatJson(once.text)
  if (!twice.ok) throw new Error('second format failed')
  eq(twice.text, once.text)
})

// ---------- minifyJson ----------

console.log('\nminifyJson')
assert('compresses multi-line JSON', () => {
  const input = '{\n  "a": 1\n}'
  const r = minifyJson(input)
  if (!r.ok) throw new Error('expected ok')
  eq(r.text, '{"a":1}')
})

assert('compresses nested array', () => {
  const input = '[\n  [1, 2],\n  [3, 4]\n]'
  const r = minifyJson(input)
  if (!r.ok) throw new Error('expected ok')
  eq(r.text, '[[1,2],[3,4]]')
})

assert('handles Chinese characters', () => {
  const r = minifyJson('{"k":"中文"}')
  if (!r.ok) throw new Error('expected ok')
  eq(r.text, '{"k":"中文"}')
})

// ---------- Error handling ----------

console.log('\nerror handling')
assert('empty string returns Empty input', () => {
  const r = formatJson('')
  eq(r, { ok: false, error: 'Empty input' })
})

assert('whitespace-only returns Empty input', () => {
  const r = formatJson('   \t\n  ')
  eq(r, { ok: false, error: 'Empty input' })
})

assert('minify empty string returns Empty input', () => {
  const r = minifyJson('')
  eq(r, { ok: false, error: 'Empty input' })
})

assert('invalid JSON returns error with position', () => {
  const r = formatJson('{a:}')
  if (r.ok) throw new Error('expected failure')
  if (!r.error.startsWith('Invalid JSON:')) {
    throw new Error(`error should start with "Invalid JSON:", got "${r.error}"`)
  }
})

assert('invalid JSON mid-string returns error', () => {
  const r = formatJson('[1, 2,')
  if (r.ok) throw new Error('expected failure')
  if (!r.error.startsWith('Invalid JSON:')) {
    throw new Error(`error should start with "Invalid JSON:", got "${r.error}"`)
  }
})

assert('minify invalid JSON returns error', () => {
  const r = minifyJson('not json')
  if (r.ok) throw new Error('expected failure')
  if (!r.error.startsWith('Invalid JSON:')) {
    throw new Error(`error should start with "Invalid JSON:", got "${r.error}"`)
  }
})

// ---------- Round-trip ----------

console.log('\nround-trip')
assert('format then minify then parse equals original object', () => {
  const original = '{"name":"test","nested":{"count":42},"list":[1,"two"]}'
  const fmt = formatJson(original)
  if (!fmt.ok) throw new Error('format failed: ' + fmt.error)
  const min = minifyJson(fmt.text)
  if (!min.ok) throw new Error('minify failed: ' + min.error)
  eq(JSON.parse(min.text), JSON.parse(original))
})

assert('complex nesting round-trip', () => {
  const obj = { a: 1, b: [2, { c: 3 }], d: 'hello', e: null, f: true }
  const input = JSON.stringify(obj)
  const fmt = formatJson(input)
  if (!fmt.ok) throw new Error('format failed')
  const min = minifyJson(fmt.text)
  if (!min.ok) throw new Error('minify failed')
  eq(JSON.parse(min.text), obj)
})

// ---------- Summary ----------

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`)
if (failed > 0) {
  throw new Error(`${failed} json-tools test(s) failed`)
}