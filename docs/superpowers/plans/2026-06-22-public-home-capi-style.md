# Public Home Single-Screen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Convert the public default home page into a single full-screen gateway entry page with a dotted globe visual.

**Architecture:** Keep the existing route and custom-home override. Put the one-screen default experience in `Hero`, reuse the existing `HeroGlobe`, and render only `Hero` for the default public home. Keep protected New API attribution visible through a compact in-hero attribution link.

**Tech Stack:** React 19, TypeScript, TanStack Router, Tailwind CSS, i18next, Bun/Rsbuild.

---

## File Structure

Modify:

- `web/default/src/features/home/index.tsx` — default public home composition; preserve loading/custom-content branches.
- `web/default/src/features/home/components/sections/hero.tsx` — complete one-screen layout.
- `web/default/src/features/home/components/hero-globe.tsx` — tune globe sizing/labels if needed.
- `web/default/src/i18n/locales/{en,zh,fr,ja,ru,vi}.json` — add/update new strings.

Do not create long-scroll sections for this iteration.

Do not modify backend files, dashboard layout, login/register pages, or pricing internals.

---

### Task 1: Single-Screen Hero Layout

**Files:**

- Modify: `web/default/src/features/home/components/sections/hero.tsx`
- Modify: `web/default/src/features/home/components/hero-globe.tsx` only if sizing/label polish is required.

- [ ] **Step 1: Replace the hero return with a full-screen layout**

`Hero` should render a single `section` with:

```tsx
<section className='relative flex min-h-svh overflow-hidden bg-background px-6 pt-24 pb-8'>
```

The layout should include:

- faint grid background,
- left copy block,
- right globe visual,
- two compact capability cards,
- one terminal/status card,
- provider/model pill row,
- compact footer attribution.

- [ ] **Step 2: Use these hero strings**

Use `t()` for all user-facing text:

```tsx
t('UNIFIED AI GATEWAY')
t('Wynth API')
t('One screen for every upstream')
t('Aggregate new-api, sub2api, account pools, monitoring, and strict priority routing behind one OpenAI-compatible API.')
t('Cost-aware routing')
t('Channel ratios, priority tiers, and monitoring signals stay visible before traffic reaches expensive upstreams.')
t('Stable fallback')
t('Retry within the current priority tier first, then fall back only when that tier is exhausted.')
t('terminal')
t('curl -X POST https://your-domain.example/v1/chat/completions')
t('200 OK')
t('{ "route": "best healthy upstream" }')
t('Supported')
t('Soon')
t('More')
t('Project attribution')
```

Provider pills:

```tsx
[
  ['OpenAI', 'Supported'],
  ['Claude', 'Soon'],
  ['Gemini', 'Soon'],
  ['Codex', 'Supported'],
  ['More', 'Soon'],
]
```

- [ ] **Step 3: Preserve CTA behavior**

Authenticated users:

```tsx
<Button render={<Link to='/dashboard' />}>{t('Go to Dashboard')}</Button>
{renderDocsButton()}
```

Anonymous users:

```tsx
<Button render={<Link to='/sign-up' />}>{t('Get Started')}</Button>
<Button variant='outline' render={<Link to='/pricing' />}>{t('View Pricing')}</Button>
{renderDocsButton()}
```

Keep the existing `docsUrl` and `renderDocsButton()` behavior.

- [ ] **Step 4: Add compact attribution inside Hero**

Add a small bottom line that preserves protected project attribution:

```tsx
<a href='https://github.com/QuantumNous/new-api' target='_blank' rel='noopener noreferrer'>
  {t('New API')}
</a>
```

The text should also mention `QuantumNous` through the URL/attribution context already visible in the link target and project identity. Do not remove existing copyright headers.

- [ ] **Step 5: Run focused check**

Run from `web/default`:

```powershell
bun run typecheck
```

Expected: exit 0.

- [ ] **Step 6: Commit**

```powershell
git add web/default/src/features/home/components/sections/hero.tsx web/default/src/features/home/components/hero-globe.tsx
git commit -m "feat: simplify public home to single screen"
```

---

### Task 2: Default Home Composition

**Files:**

- Modify: `web/default/src/features/home/index.tsx`
- Modify: `web/default/src/features/home/components/index.ts` only if imports require cleanup.

- [ ] **Step 1: Remove long-section imports**

`Home` should no longer import or render:

- `Stats`
- `Features`
- `HowItWorks`
- `CTA`
- `Footer`

Keep `Hero`.

- [ ] **Step 2: Preserve loading and custom content branches**

Do not change:

- `if (!isLoaded) ...`
- `if (content) ...`

- [ ] **Step 3: Render only Hero for default home**

Default return:

```tsx
return (
  <PublicLayout showMainContainer={false}>
    <Hero isAuthenticated={isAuthenticated} />
  </PublicLayout>
)
```

- [ ] **Step 4: Run focused check**

Run from `web/default`:

```powershell
bun run typecheck
```

Expected: exit 0.

- [ ] **Step 5: Commit**

```powershell
git add web/default/src/features/home/index.tsx web/default/src/features/home/components/index.ts
git commit -m "feat: render public home as single screen"
```

---

### Task 3: i18n

**Files:**

- Modify: `web/default/src/i18n/locales/en.json`
- Modify: `web/default/src/i18n/locales/zh.json`
- Modify: `web/default/src/i18n/locales/fr.json`
- Modify: `web/default/src/i18n/locales/ja.json`
- Modify: `web/default/src/i18n/locales/ru.json`
- Modify: `web/default/src/i18n/locales/vi.json`

- [ ] **Step 1: Run sync**

Run from `web/default`:

```powershell
bun run i18n:sync
```

- [ ] **Step 2: Add real translations**

At minimum, ensure these zh translations:

```json
{
  "UNIFIED AI GATEWAY": "统一 AI 网关",
  "One screen for every upstream": "一个屏幕，管理所有上游",
  "Aggregate new-api, sub2api, account pools, monitoring, and strict priority routing behind one OpenAI-compatible API.": "把 new-api、sub2api、账号池、监控和严格优先级调度聚合到一个 OpenAI 兼容 API 后面。",
  "Cost-aware routing": "成本感知路由",
  "Channel ratios, priority tiers, and monitoring signals stay visible before traffic reaches expensive upstreams.": "在流量进入昂贵上游前，先看清渠道倍率、优先级层级和监控信号。",
  "Stable fallback": "稳定故障转移",
  "Retry within the current priority tier first, then fall back only when that tier is exhausted.": "优先在当前优先级层内重试，只有该层耗尽后才降级到下一层。",
  "Project attribution": "项目归属"
}
```

Other locales should have reasonable direct translations and no empty values.

- [ ] **Step 3: Re-run sync**

```powershell
bun run i18n:sync
```

Expected: no missing/untranslated/extraneous keys in the sync report.

- [ ] **Step 4: Commit**

```powershell
git add web/default/src/i18n/locales/en.json web/default/src/i18n/locales/zh.json web/default/src/i18n/locales/fr.json web/default/src/i18n/locales/ja.json web/default/src/i18n/locales/ru.json web/default/src/i18n/locales/vi.json
git commit -m "feat: translate single-screen public home"
```

---

### Task 4: Build and Visual QA

**Files:**

- Source edits only if verification finds concrete defects.

- [ ] **Step 1: Run final checks**

Run from `web/default`:

```powershell
bun run typecheck
bun run build
```

Expected: both exit 0.

- [ ] **Step 2: Start or reuse dev server**

Run from `web/default`:

```powershell
bun run dev --host 127.0.0.1 --port 3001
```

If port `3001` is occupied by another branch, use `3002`.

- [ ] **Step 3: Desktop screenshot QA**

At `1440x900`, verify:

- Page feels like one screen, not a long landing page.
- Globe is visible and framed.
- Hero text, CTAs, cards, terminal card, provider pills, and attribution fit.
- Protected `New API` attribution link is visible.

- [ ] **Step 4: Mobile screenshot QA**

At `390x844`, verify:

- No text overlap.
- CTA buttons fit.
- Globe and cards remain readable.
- Slight vertical scroll is acceptable, but it should not feel like separate landing sections.

- [ ] **Step 5: Dark mode screenshot QA**

Verify:

- Globe labels are readable.
- Cards and terminal contrast are consistent.
- No theme mismatch.

- [ ] **Step 6: Commit fixes if needed**

```powershell
git add web/default/src/features/home web/default/src/i18n/locales
git commit -m "fix: polish single-screen public home"
```

Skip if no fixes were required.

---

### Task 5: Claude Review and Push

**Files:**

- No source edits expected unless review finds a concrete issue.

- [ ] **Step 1: Ask Claude for low-cost review**

Run from repo root:

```powershell
git diff upstream-source-sync > .codex/public-home-single-screen.diff
Get-Content -Raw .codex/public-home-single-screen.diff | claude -p --model sonnet --effort low --output-format json --disable-slash-commands --tools ""
```

If default Claude quota is unavailable, retry with the configured settings file only if needed.

- [ ] **Step 2: Evaluate Claude findings**

Apply only findings that are correct, scoped, and compatible with the single-screen requirement.

- [ ] **Step 3: Final verification**

Run:

```powershell
bun run typecheck
bun run i18n:sync
bun run build
git diff --check
```

Expected: all exit 0.

- [ ] **Step 4: Push branch**

```powershell
git push origin public-home-capi-style
```

---

## Self-Review

Spec coverage:

- Single-screen home: Task 1 and Task 2.
- Globe anchor: Task 1.
- No long sections: Task 2.
- i18n: Task 3.
- Desktop/mobile/dark QA: Task 4.
- Claude review and push: Task 5.

Scope guard:

- No backend files.
- Dashboard, login, register, and pricing internals stay untouched.
- Custom home content branch is preserved.
- Protected New API attribution remains visible.
