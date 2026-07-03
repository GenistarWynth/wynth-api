# Frontend i18n Backfill + Guardrail — Design

- **Date:** 2026-07-03
- **Status:** Approved (design); pending spec review.
- **Scope:** Issue #1 of the three-issue batch. Branch `feat/i18n-backfill-guardrail`, off merged `main` (`c23efd48`, which already contains #2 and #3).

## Problem

~234 `t('English string')` calls reference keys that were never added to `en.json`, so i18next looks them up in the active locale → misses → falls back to `en` → also misses → returns the key literal (the English string). These strings therefore render **English in every language** (including zh), and English UI can never reveal the defect. `bun run i18n:sync` cannot catch it because it only diffs locale JSON files against each other; it never scans source for `t()` calls, and there is no lint/CI rule for it.

## Grounding facts (measured on current `main`, this worktree)

1. Locale files are **nested under a `"translation"` namespace**: `en.json` = `{ "translation": { <5117 flat keys> } }`. i18next config (`web/default/src/i18n/config.ts`) imports each locale and uses `nsSeparator: false`; keys are English source strings. This nesting must be handled by any scanner (compare against `en.translation`, not the top level).
2. **234 `t()` keys are referenced in `web/default/src` but absent from `en.translation`.** Worst files: `features/account-pools/index.tsx` (57), `features/channels/components/dialogs/codex-usage-dialog.tsx` (31), `features/system-info/components/system-instances-panel.tsx` (15) + `system-tasks-panel.tsx` (14), `features/dashboard/components/flow/flow-charts.tsx` (12) + `flow-node-filter.tsx` (8), `features/system-settings/models/routing-reliability-section.tsx` (9), `.../integrations/email-settings-section.tsx` (7), and a long tail across system-settings/users/models/playground/auth.
3. Some missing keys carry i18next **interpolation** (`{{count}}`, `{{duration}}`, `{{id}}`, `{{skipped}}`, `{{failed}}`) — these are legitimate keys; the placeholders must be preserved verbatim in every locale.
4. `zh.json` currently has **full key parity with en** (0 missing), so once the 234 keys are added to `en`, they must also be added to `zh` (with Chinese) — otherwise `i18n:sync` propagates them to `zh` as English placeholders.
5. 36 existing `zh` values are byte-identical to their English keys (a small pre-existing under-translation, mostly acronyms/brand; a handful are genuine).
6. `#2`/`#3`'s own frontend strings are all present in `en.json` — they are NOT part of the 234 (those tasks added keys correctly).
7. Tooling: `web/default/package.json` has `i18n:sync` (`node scripts/sync-i18n.mjs` — reorders + fills each locale's missing keys FROM en) and `lint` (`oxlint`, no i18n plugin). No source-scanning check exists.

## Goals

- Add the 234 missing keys to `en.json` (value = the English key, per i18next convention) so they resolve in English reliably and become translatable.
- Translate the 234 keys into `zh.json` (Chinese), matching existing terminology, preserving interpolation placeholders.
- Propagate the 234 keys to `fr/ru/ja/vi` as English placeholders (via the existing `i18n:sync`), consistent with those locales' current partial state.
- Add a **source-scanning guardrail** (`i18n:check`) that fails when a `t('literal')` key is missing from `en.json`, wired into CI, so the defect cannot recur.

## Non-goals

- Full translation of `fr/ru/ja/vi` for the new keys (English placeholders only, per decision).
- Refactoring the locale files out of the `"translation"` nesting.
- Fixing hardcoded (non-`t()`) JSX strings or `aria-label`s — out of scope (a separate, smaller class; the 234 `t()`-key gap is the user-visible issue).
- Changing `i18n:sync`'s behavior beyond running it.

## Design

### 1. Guardrail scanner — `web/default/scripts/check-i18n-keys.mjs`
A Node ESM script (matches the existing `scripts/*.mjs` style) that:
- Loads `src/i18n/locales/en.json` and reads its `.translation` object (fallback to top-level if not nested, for safety).
- Walks `src/**/*.{ts,tsx,js,jsx}` (skipping `node_modules`, `dist`, and `src/i18n/locales`).
- Extracts keys from `t(` calls via a regex that matches a single quoted string literal as the FIRST argument: `/\bt\(\s*(['"`])((?:\\.|(?!\1)[^\\])*?)\1/g`.
- **Skips** matches whose key contains `${` (a JS template literal with interpolation — dynamic, uncheckable) and any non-string-literal first arg (dynamic `t(variable)` simply won't match the regex).
- **Keeps** i18next `{{...}}` interpolation keys (they are static literals).
- Diffs the extracted static keys against `en.translation`; collects those missing, grouped by file.
- Prints a clear report (count + `file: "key"` lines) and exits `1` if any are missing, `0` otherwise.
- Add `"i18n:check": "node scripts/check-i18n-keys.mjs"` to `web/default/package.json` scripts.
- **CI wiring:** add an `i18n:check` step to the frontend CI workflow under `.github/workflows/` (run it where `bun`/`node` frontend steps already run, e.g. alongside lint/typecheck). If no suitable frontend workflow step exists, add the script and a minimal CI step; do not restructure existing CI.

Known limitation (documented in the script header + spec): only STATIC `t('literal')` calls are checked; dynamically-built keys (`t(someVar)`, `t(\`x-${y}\`)`) are intentionally not verifiable and are skipped. This is acceptable — the 234-key regression class is entirely static literals.

### 2. Backfill `en.json` + propagate
- Add all 234 missing keys to `en.json`'s `translation` object with value equal to the key (English source string), inserted in the file's existing ordering convention (the sync tool reorders anyway).
- Run `bun run i18n:sync` (from `web/default/`) to reorder all locales to en's key order and fill `fr/ru/ja/vi` (and `zh`) missing keys from en as placeholders.
- After this, `bun run i18n:check` exits 0.

### 3. Translate `zh.json`
- For each of the 234 keys, set the `zh.translation` value to a natural Chinese translation, consistent with existing zh terminology (this is an AI-API-gateway admin dashboard: channels/渠道, tokens/令牌, groups/分组, quota/额度, model/模型, upstream source/上游源, account pool/账号池, etc.).
- **Preserve interpolation placeholders exactly** (`{{count}}`, `{{duration}}`, `{{id}}`, `{{skipped}}`, `{{failed}}`) — never translate or reorder the `{{...}}` tokens themselves.
- Optionally fix the 36 byte-identical-to-en zh values that are genuine phrases (skip brand names / acronyms / model IDs).
- Keep `en/zh/fr/ru/ja/vi` at full key parity (verify counts match after).

## Testing / verification

- `bun run i18n:check` — exits 1 before backfill (lists 234), exits 0 after (Task 2 delivers this transition; it is the guardrail's own red→green).
- `bun run typecheck` (tsgo) exits 0 (no code type changes expected, but locale JSON imports must stay valid).
- All six locale JSON files parse (`JSON.parse`) and have equal key counts under `translation`.
- `bun run i18n:sync` produces no further drift after backfill (idempotent).
- Spot-check a sample of zh translations for correctness + placeholder preservation.
- The guardrail script's regex is validated against representative inputs (single/double/backtick quotes, `${}` skip, `{{}}` keep, a `t(variable)` non-match) — a tiny unit check or inline self-test.

## Decisions
- **Backfill `en` + full `zh` translation + CI guardrail.** *(user)*
- Other locales (`fr/ru/ja/vi`) get English placeholders via `i18n:sync`, not full translation. *(user)*
- Scanner checks only static `t('literal')` keys; dynamic keys are skipped by design. *(chosen)*
- Locale nesting under `"translation"` is preserved; the scanner reads `.translation`. *(measured constraint)*

## Risks & mitigations
- **zh translation quality/consistency** (234 strings) → translate against the existing zh.json terminology; preserve placeholders; spot-check; keep parity counts equal.
- **Scanner false positives** (flagging a dynamic or legitimately-absent key) → the regex only matches string-literal first args; `${}` skipped; documented limitation. Validate against representative inputs.
- **`i18n:sync` reordering churn** → expected; run it once as part of Task 2 so the diff is deterministic.
- **CI placement** → wire minimally into the existing frontend workflow; do not restructure CI.
