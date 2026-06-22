# Public Home Page C-API Style Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign the public default home page around a c-api.cc-inspired light landing page with a dotted globe/network hero visual.

**Architecture:** Keep the existing `Home` route and custom home content override. Replace only the default public landing sections under `web/default/src/features/home`, using small focused React components and existing UI primitives. The hero globe is local SVG/CSS/HTML, not a remote image and not copied from the reference asset.

**Tech Stack:** React 19, TypeScript, TanStack Router, Base UI wrappers, Tailwind CSS, i18next, Bun/Rsbuild.

---

## File Structure

Modify:

- `web/default/src/features/home/index.tsx` — default section composition only; preserve loading/custom-content branches.
- `web/default/src/features/home/components/index.ts` — export new section components.
- `web/default/src/features/home/components/sections/hero.tsx` — replace terminal-heavy hero with copy + globe.
- `web/default/src/features/home/components/sections/features.tsx` — make this the practical capabilities/tools section.
- `web/default/src/features/home/components/sections/cta.tsx` — update closing CTA copy/style.
- `web/default/src/i18n/locales/{en,zh,fr,ja,ru,vi}.json` — add all new source keys.

Create:

- `web/default/src/features/home/components/hero-globe.tsx` — local dotted globe/network visual.
- `web/default/src/features/home/components/landing-card.tsx` — small reusable card shell for landing sections.
- `web/default/src/features/home/components/sections/ecosystem.tsx` — supported protocols/tools cards.
- `web/default/src/features/home/components/sections/pricing-preview.tsx` — static pricing/value preview cards linking to `/pricing`.
- `web/default/src/features/home/components/sections/value.tsx` — operational value cards.
- `web/default/src/features/home/components/sections/faq.tsx` — FAQ accordion using existing `components/ui/accordion`.

Remove from default home composition:

- Standalone `Stats` section, unless a later implementation finds the numbers are still useful inside another section.
- Standalone `HowItWorks` section. Its useful ideas should be folded into `Value`.

No backend files should change.

---

### Task 1: Hero Globe Visual

**Files:**

- Create: `web/default/src/features/home/components/hero-globe.tsx`
- Modify: `web/default/src/features/home/components/sections/hero.tsx`
- Modify: `web/default/src/features/home/components/index.ts`

- [ ] **Step 1: Create the local globe component**

Create `hero-globe.tsx` with the existing AGPL header and this component shape:

```tsx
import { cn } from '@/lib/utils'

type HeroGlobeProps = {
  className?: string
}

const nodes = [
  { label: 'OpenAI', x: 31, y: 30 },
  { label: 'Claude', x: 68, y: 25 },
  { label: 'Gemini', x: 76, y: 42 },
  { label: 'Codex', x: 54, y: 19 },
  { label: 'Users', x: 23, y: 68 },
]

export function HeroGlobe({ className }: HeroGlobeProps) {
  return (
    <div
      className={cn(
        'relative mx-auto aspect-square w-full max-w-[25rem] md:max-w-[28rem]',
        className
      )}
      aria-label='Global upstream routing illustration'
    >
      <div className='absolute inset-[8%] rounded-full bg-[radial-gradient(circle_at_35%_35%,var(--card)_0%,var(--muted)_42%,transparent_72%)] shadow-[inset_-24px_-28px_56px_rgba(15,23,42,0.08),0_28px_80px_rgba(15,23,42,0.08)] dark:shadow-[inset_-24px_-28px_56px_rgba(0,0,0,0.28),0_28px_80px_rgba(0,0,0,0.22)]' />
      <svg
        className='absolute inset-0 size-full text-primary/45 dark:text-primary/35'
        viewBox='0 0 100 100'
        role='img'
        aria-hidden='true'
      >
        <defs>
          <pattern id='hero-globe-dots' width='4' height='4' patternUnits='userSpaceOnUse'>
            <circle cx='1' cy='1' r='0.35' fill='currentColor' />
          </pattern>
          <clipPath id='hero-globe-clip'>
            <circle cx='50' cy='50' r='39' />
          </clipPath>
        </defs>
        <circle cx='50' cy='50' r='39' fill='url(#hero-globe-dots)' clipPath='url(#hero-globe-clip)' />
        <ellipse cx='50' cy='50' rx='38' ry='12' fill='none' stroke='currentColor' strokeWidth='0.35' opacity='0.35' />
        <ellipse cx='50' cy='50' rx='24' ry='38' fill='none' stroke='currentColor' strokeWidth='0.35' opacity='0.3' />
        <path d='M23 68 C35 58 39 52 50 50 C60 47 69 33 76 42' fill='none' stroke='currentColor' strokeWidth='0.55' strokeDasharray='1.5 1.8' />
        <path d='M31 30 C38 35 43 43 50 50 C57 43 62 31 68 25' fill='none' stroke='currentColor' strokeWidth='0.55' strokeDasharray='1.5 1.8' />
      </svg>
      <div className='absolute top-1/2 left-1/2 flex -translate-x-1/2 -translate-y-1/2 items-center rounded-full border border-primary/20 bg-background/85 px-3 py-1 text-xs font-semibold text-primary shadow-sm backdrop-blur'>
        Wynth API
      </div>
      {nodes.map((node) => (
        <div
          key={node.label}
          className='absolute rounded-full border border-border/70 bg-background/90 px-2.5 py-1 text-[11px] font-medium text-foreground shadow-sm backdrop-blur'
          style={{ left: `${node.x}%`, top: `${node.y}%` }}
        >
          {node.label}
        </div>
      ))}
    </div>
  )
}
```

- [ ] **Step 2: Replace the hero terminal import**

In `hero.tsx`, remove:

```tsx
import { CherryStudio } from '@lobehub/icons'
import { HeroTerminalDemo } from '../hero-terminal-demo'
```

Add:

```tsx
import { HeroGlobe } from '../hero-globe'
```

Keep `ArrowRight`, `BookOpen`, `useStatus`, and `Button`.

- [ ] **Step 3: Rewrite the hero layout**

Replace the return body in `Hero` with a c-api-like two-column hero:

```tsx
return (
  <section className='relative z-10 overflow-hidden bg-[linear-gradient(180deg,var(--background)_0%,color-mix(in_oklch,var(--muted)_55%,var(--background))_100%)] px-6 pt-28 pb-20 md:pt-34 md:pb-28'>
    <div className='mx-auto grid max-w-6xl items-center gap-12 lg:grid-cols-[1.02fr_0.98fr]'>
      <div className='max-w-2xl text-left'>
        <div className='landing-animate-fade-up mb-5 inline-flex items-center rounded-full bg-primary/5 px-3 py-1.5 text-[11px] font-semibold tracking-[0.16em] text-primary uppercase opacity-0'>
          {t('THE UNIVERSAL AI GATEWAY')}
        </div>
        <h1 className='landing-animate-fade-up max-w-[13ch] text-[clamp(2.6rem,5vw,4.75rem)] leading-[1.03] font-semibold opacity-0' style={{ animationDelay: '60ms' }}>
          {t('Connect every upstream through one AI gateway')}
        </h1>
        <p className='landing-animate-fade-up text-muted-foreground mt-6 max-w-xl text-base leading-7 opacity-0 md:text-lg' style={{ animationDelay: '120ms' }}>
          {t('Aggregate new-api, sub2api, account pools, monitoring, and strict priority routing behind one OpenAI-compatible API.')}
        </p>
        <div className='landing-animate-fade-up mt-8 flex flex-wrap items-center gap-3 opacity-0' style={{ animationDelay: '180ms' }}>
          {/* keep the existing authenticated/anonymous CTA branching here */}
        </div>
      </div>
      <div className='landing-animate-fade-up relative flex justify-center opacity-0' style={{ animationDelay: '240ms' }}>
        <HeroGlobe />
      </div>
    </div>
  </section>
)
```

Preserve the existing `renderDocsButton()` behavior and CTA branch. Anonymous users keep `Get Started`, `View Pricing`, `Docs`; authenticated users keep `Go to Dashboard`, `Docs`.

- [ ] **Step 4: Run focused checks**

Run:

```powershell
bun run typecheck
```

Expected: exit 0.

- [ ] **Step 5: Commit**

```powershell
git add web/default/src/features/home/components/hero-globe.tsx web/default/src/features/home/components/sections/hero.tsx web/default/src/features/home/components/index.ts
git commit -m "feat: add globe hero to public home"
```

---

### Task 2: Landing Section Components

**Files:**

- Create: `web/default/src/features/home/components/landing-card.tsx`
- Modify: `web/default/src/features/home/components/sections/features.tsx`
- Create: `web/default/src/features/home/components/sections/ecosystem.tsx`
- Create: `web/default/src/features/home/components/sections/pricing-preview.tsx`
- Create: `web/default/src/features/home/components/sections/value.tsx`
- Create: `web/default/src/features/home/components/sections/faq.tsx`
- Modify: `web/default/src/features/home/components/index.ts`

- [ ] **Step 1: Add reusable landing card**

Create `landing-card.tsx`:

```tsx
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

type LandingCardProps = {
  children: ReactNode
  className?: string
}

export function LandingCard({ children, className }: LandingCardProps) {
  return (
    <div
      className={cn(
        'rounded-2xl border border-border/70 bg-card/80 p-6 shadow-[0_18px_60px_rgba(15,23,42,0.06)] backdrop-blur transition-colors hover:border-primary/25 dark:shadow-[0_18px_60px_rgba(0,0,0,0.22)]',
        className
      )}
    >
      {children}
    </div>
  )
}
```

- [ ] **Step 2: Rewrite `Features` as practical capability cards**

Use four cards:

```tsx
const features = [
  ['Multi-upstream aggregation', 'Connect new-api, sub2api, and private account pools as upstream channels.'],
  ['Channel monitoring', 'Record availability, latency, first-token latency, and cache behavior for routing decisions.'],
  ['Strict priority routing', 'Exhaust the current priority tier before falling back to lower priority channels.'],
  ['Automated upstream sync', 'Discover groups, models, ratios, and generated channels from upstream sources.'],
]
```

Render them in a `max-w-6xl` two-column grid with an eyebrow `Open Source / Tools` and heading `Built for upstream aggregation`.

- [ ] **Step 3: Add ecosystem section**

Create `ecosystem.tsx` with four cards:

```tsx
const items = [
  ['OpenAI-compatible API', 'Use existing OpenAI SDKs and clients with a unified base URL.'],
  ['Claude and Anthropic flows', 'Route Claude-style workloads while keeping channel controls centralized.'],
  ['Codex and Claude Code', 'Use coding agents through monitored and prioritized upstream channels.'],
  ['Gemini and more providers', 'Keep additional model providers behind one operational surface.'],
]
```

Use lucide icons such as `Code2`, `Bot`, `Terminal`, `Network`.

- [ ] **Step 4: Add pricing preview section**

Create `pricing-preview.tsx` with three static cards linking to `/pricing`:

```tsx
const cards = [
  ['PAYGO', 'Pay as you go', 'Use gateway quota only when traffic is routed.'],
  ['Channel ratios', 'Price-aware upstreams', 'Compare upstream multipliers before routing traffic.'],
  ['Priority strategy', 'Cost-first failover', 'Prefer cheaper healthy channels before expensive fallbacks.'],
]
```

Use `Button` with `render={<Link to='/pricing' />}` for the main action.

- [ ] **Step 5: Add value section**

Create `value.tsx` with four compact horizontal cards:

```tsx
const values = [
  ['Lower operational cost', 'Let channel ratios and priority controls shape traffic before it reaches expensive upstreams.'],
  ['Availability memory', 'Use recent monitoring history to understand which routes are actually reliable.'],
  ['Unified management', 'Manage keys, models, groups, upstreams, and routing from one place.'],
  ['Self-host friendly', 'Keep deployment and data ownership under your control.'],
]
```

- [ ] **Step 6: Add FAQ section**

Create `faq.tsx` using existing accordion primitives:

```tsx
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
```

Use these questions:

```tsx
const faqs = [
  ['Which upstreams can Wynth API connect to?', 'It is designed around new-api and sub2api style upstreams first, with room for more provider adapters.'],
  ['Can I keep using OpenAI-compatible clients?', 'Yes. The landing page should state compatibility without promising every provider feature is identical.'],
  ['How do priority and weight work?', 'Higher priority channels are tried first; weight only decides among channels in the same priority tier.'],
  ['Can I self-host it?', 'Yes. The project remains open-source and self-host oriented.'],
]
```

- [ ] **Step 7: Export new sections**

Update `components/index.ts`:

```tsx
export { CTA } from './sections/cta'
export { Ecosystem } from './sections/ecosystem'
export { FAQ } from './sections/faq'
export { Features } from './sections/features'
export { Hero } from './sections/hero'
export { PricingPreview } from './sections/pricing-preview'
export { Value } from './sections/value'
```

- [ ] **Step 8: Run focused checks**

Run:

```powershell
bun run typecheck
```

Expected: exit 0.

- [ ] **Step 9: Commit**

```powershell
git add web/default/src/features/home/components
git commit -m "feat: add public home landing sections"
```

---

### Task 3: Compose Default Home Page

**Files:**

- Modify: `web/default/src/features/home/index.tsx`
- Modify: `web/default/src/features/home/components/sections/cta.tsx`

- [ ] **Step 1: Update section imports**

Change the home imports to:

```tsx
import { CTA, Ecosystem, FAQ, Features, Hero, PricingPreview, Value } from './components'
```

Remove `HowItWorks` and `Stats` from the default composition.

- [ ] **Step 2: Update default section order**

Default home should render:

```tsx
<PublicLayout showMainContainer={false}>
  <Hero isAuthenticated={isAuthenticated} />
  <Features />
  <Ecosystem />
  <PricingPreview />
  <Value />
  <FAQ />
  <CTA isAuthenticated={isAuthenticated} />
  <Footer />
</PublicLayout>
```

Keep the `!isLoaded` branch and the `if (content)` branch unchanged.

- [ ] **Step 3: Update CTA copy**

Change the closing CTA to use:

```tsx
{t('Ready to route AI traffic with more control?')}
```

and body:

```tsx
{t('Create a gateway, connect upstreams, and start managing cost, availability, and routing from one place.')}
```

Keep authenticated users returning `null`.

- [ ] **Step 4: Run checks**

Run:

```powershell
bun run typecheck
```

Expected: exit 0.

- [ ] **Step 5: Commit**

```powershell
git add web/default/src/features/home/index.tsx web/default/src/features/home/components/sections/cta.tsx
git commit -m "feat: compose c-api style public home"
```

---

### Task 4: i18n

**Files:**

- Modify: `web/default/src/i18n/locales/en.json`
- Modify: `web/default/src/i18n/locales/zh.json`
- Modify: `web/default/src/i18n/locales/fr.json`
- Modify: `web/default/src/i18n/locales/ja.json`
- Modify: `web/default/src/i18n/locales/ru.json`
- Modify: `web/default/src/i18n/locales/vi.json`

- [ ] **Step 1: Run i18n sync**

Run:

```powershell
bun run i18n:sync
```

Expected: new source keys appear in all locale files and the report has no missing keys after translation edits.

- [ ] **Step 2: Add real zh translations**

At minimum, translate the new hero and section keys:

```json
{
  "THE UNIVERSAL AI GATEWAY": "通用 AI 网关",
  "Connect every upstream through one AI gateway": "用一个 AI 网关连接所有上游",
  "Aggregate new-api, sub2api, account pools, monitoring, and strict priority routing behind one OpenAI-compatible API.": "把 new-api、sub2api、账号池、监控和严格优先级调度聚合到一个 OpenAI 兼容 API 后面。",
  "Built for upstream aggregation": "为上游聚合而生",
  "Multi-upstream aggregation": "多上游聚合",
  "Channel monitoring": "渠道监控",
  "Strict priority routing": "严格优先级调度",
  "Automated upstream sync": "自动上游同步",
  "Supported devices and AI coding tools": "支持的设备与 AI 编程工具",
  "Pricing that follows your routing strategy": "跟随路由策略的价格入口",
  "Ready to route AI traffic with more control?": "准备好更可控地调度 AI 流量了吗？"
}
```

- [ ] **Step 3: Add reasonable non-zh translations**

Use concise translations for `fr`, `ja`, `ru`, and `vi`. Do not leave empty values. If unsure, prefer a clear direct translation over untranslated English.

- [ ] **Step 4: Re-run i18n sync**

Run:

```powershell
bun run i18n:sync
```

Expected: report shows no missing/untranslated/extraneous keys for supported locales.

- [ ] **Step 5: Commit**

```powershell
git add web/default/src/i18n/locales/en.json web/default/src/i18n/locales/zh.json web/default/src/i18n/locales/fr.json web/default/src/i18n/locales/ja.json web/default/src/i18n/locales/ru.json web/default/src/i18n/locales/vi.json
git commit -m "feat: translate public home landing copy"
```

---

### Task 5: Build and Visual QA

**Files:**

- No source edits expected unless verification finds a concrete issue.

- [ ] **Step 1: Run final frontend checks**

Run:

```powershell
bun run typecheck
bun run build
```

Expected: both exit 0.

- [ ] **Step 2: Start or reuse the local dev server**

Run from `web/default`:

```powershell
bun run dev --host 127.0.0.1 --port 3001
```

If port `3001` is already running the current branch, reuse it. If it is occupied by another process, use `3002`.

- [ ] **Step 3: Capture desktop screenshot**

Use browser tooling against `/` at `1440x1100`. Verify:

- Globe is visible in the first viewport.
- Hero text and CTA are not overlapped.
- Header remains usable.
- The page does not use the reference site's brand, logo, or copied text.

- [ ] **Step 4: Capture mobile screenshot**

Use browser tooling against `/` at `390x844`. Verify:

- Globe stacks below hero copy.
- CTA buttons fit without text clipping.
- Cards stack without overlap.
- FAQ accordion is reachable.

- [ ] **Step 5: Capture dark mode screenshot**

Switch to dark mode and verify:

- Globe labels remain readable.
- Card surfaces are theme-consistent.
- No black-on-white or white-on-white mismatches.

- [ ] **Step 6: Fix visual defects if found**

Only fix concrete defects observed in screenshots. Re-run the relevant screenshot after each fix.

- [ ] **Step 7: Commit final visual fixes**

If any fixes were required:

```powershell
git add web/default/src/features/home web/default/src/i18n/locales
git commit -m "fix: polish public home responsive visuals"
```

If no fixes were required, skip this commit.

---

### Task 6: Review and Push

**Files:**

- No source edits expected unless review finds a concrete issue.

- [ ] **Step 1: Request Claude review**

Run a low-cost Claude review of the branch diff:

```powershell
git diff upstream-source-sync > .codex/public-home-capi-style.diff
Get-Content -Raw .codex/public-home-capi-style.diff | claude -p --model sonnet --effort low --output-format json --disable-slash-commands --tools ""
```

If default Claude quota is unavailable, retry with the previously configured settings file only if needed.

- [ ] **Step 2: Evaluate Claude findings**

Apply only findings that are technically correct for this codebase and within the public-home scope.

- [ ] **Step 3: Final checks**

Run:

```powershell
bun run typecheck
bun run i18n:sync
bun run build
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 4: Push branch**

```powershell
git push origin public-home-capi-style
```

---

## Self-Review

Spec coverage:

- Globe hero: Task 1.
- C-api-like section structure: Tasks 2 and 3.
- Static pricing preview: Task 2.
- FAQ accordion: Task 2.
- i18n: Task 4.
- Desktop/mobile/dark visual QA: Task 5.
- Claude review: Task 6.

Scope guard:

- No backend files are included.
- Dashboard, login, register, and pricing internals are outside the task list.
- Custom home content branch is explicitly preserved.
- Reference globe is recreated locally with SVG/CSS rather than copied or remotely loaded.
