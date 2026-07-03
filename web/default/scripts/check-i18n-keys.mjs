#!/usr/bin/env node
// Guardrail: fail if any static t('literal') key referenced in src is missing
// from en.json. Locale files nest keys under a "translation" namespace.
//
// LIMITATIONS (static analysis, not a real parser):
// - Dynamic keys (t(variable), t(`x-${y}`)) are intentionally skipped
//   (uncheckable statically).
// - Constant-reference keys (t(SOME_CONST)) are not checked — the first arg
//   must be a literal.
// - Concatenation / partial literals (t('prefix.' + code)) are not checked —
//   the literal must be the complete first argument (closing quote followed
//   by optional whitespace then `,` or `)`).
// - Exotic escapes in key literals (e.g. `\n`, `\uXXXX`) are not decoded;
//   only escaped quote/backslash characters are unescaped before comparing
//   against en.json keys.
// - t('...') written inside a `//` or `/* */` comment is still treated as a
//   real call (comments are not stripped, to avoid corrupting string
//   contents such as `https://`), so it may false-positive as "missing".
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(here, '..')
const srcDir = path.join(root, 'src')
const enPath = path.join(root, 'src/i18n/locales/en.json')
const listMode = process.argv.includes('--list')

const enRaw = JSON.parse(fs.readFileSync(enPath, 'utf8'))
const en = enRaw.translation ?? enRaw
const keys = new Set(Object.keys(en))

// First arg of t(...) as a single/double/backtick string literal. The
// lookahead requires the closing quote to be the end of the argument (only
// whitespace then `,` or `)` may follow) so `t('key')` / `t('key', opts)`
// match but `t('prefix.' + code)` does not.
const T_CALL = /\bt\(\s*(['"`])((?:\\.|(?!\1)[^\\])*?)\1(?=\s*[,)])/g

// Unescape only escaped quote/backslash chars so `t('It\'s saved')` compares
// against the JSON key `It's saved`. Other escapes (\n, \uXXXX, etc.) are
// left as-is — see LIMITATIONS above.
function unescapeKey(raw) {
  return raw.replace(/\\(['"`\\])/g, '$1')
}

const missing = new Map() // key -> Set<relpath>
function walk(dir) {
  for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, ent.name)
    if (ent.isDirectory()) {
      if (ent.name === 'node_modules' || ent.name === 'dist') continue
      if (p.includes(path.join('i18n', 'locales'))) continue
      walk(p)
    } else if (/\.(tsx?|jsx?)$/.test(ent.name)) {
      const s = fs.readFileSync(p, 'utf8')
      let m
      while ((m = T_CALL.exec(s))) {
        const rawKey = m[2]
        if (!rawKey) continue
        if (rawKey.includes('${')) continue // JS template interpolation => dynamic
        const key = unescapeKey(rawKey)
        if (!keys.has(key)) {
          if (!missing.has(key)) missing.set(key, new Set())
          missing.get(key).add(path.relative(root, p).replace(/\\/g, '/'))
        }
      }
    }
  }
}
walk(srcDir)

if (listMode) {
  for (const k of [...missing.keys()].sort()) process.stdout.write(k + '\n')
  process.exit(missing.size ? 1 : 0)
}

if (missing.size === 0) {
  console.log(`i18n:check OK — all t() literal keys present in en.json (${keys.size} keys).`)
  process.exit(0)
}
console.error(`i18n:check FAILED — ${missing.size} t() key(s) missing from en.json:\n`)
const byFile = new Map()
for (const [k, files] of missing) for (const f of files) {
  if (!byFile.has(f)) byFile.set(f, [])
  byFile.get(f).push(k)
}
for (const f of [...byFile.keys()].sort()) {
  console.error(`  ${f}`)
  for (const k of byFile.get(f).sort()) console.error(`      ${JSON.stringify(k)}`)
}
console.error(`\nAdd these keys to src/i18n/locales/en.json (under "translation") and translate zh.json.`)
process.exit(1)
