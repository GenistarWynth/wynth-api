# Public Home Single-Screen Redesign

## Goal

Redesign only the public default home page (`/`) as a single full-screen entry page. The page should combine the dotted globe/network visual direction from `https://c-api.cc/` with the compact one-viewport structure of `https://ikun.love/home`.

This is a public landing-page update, not a dashboard/control-panel redesign.

## Scope

In scope:

- Default home page shown when no custom home page content is configured.
- Public home hero composition, visual hierarchy, responsive behavior, and i18n strings.
- A compact footer/attribution area inside the first viewport.
- Visual QA across desktop, mobile, and dark mode.

Out of scope:

- Authenticated dashboard layout.
- Login/register pages.
- Backend behavior.
- Pricing route internals.
- Long scrolling marketing sections.
- Custom home page iframe/markdown override behavior.
- Protected project identity, license headers, package metadata, and existing attribution.

## Reference Traits

Use `c-api.cc` for:

- Dotted globe/network hero visual.
- Small provider/client labels and route lines around a central gateway.
- Clean light-first AI API positioning.

Use `ikun.love/home` for:

- Single full-screen page instead of a long landing page.
- Lightweight header, large title, short subtitle, one primary CTA group.
- A few compact capability cards and a small terminal/status card inside the same viewport.
- Provider/model pills near the bottom of the hero.
- Footer text anchored in the first screen.

Do not copy either site's brand, logo, mascot, text, or images.

## Page Structure

The default home page keeps `PublicLayout` and the custom-content branch in `features/home/index.tsx`.

Default public home composition:

1. `Hero`
   - Full viewport (`min-h-svh`) single page.
   - Left/top copy:
     - small uppercase badge,
     - large H1 focused on Wynth API as a unified upstream gateway,
     - short value statement,
     - CTAs.
   - Right/visual area:
     - local dotted globe/network visual,
     - a compact terminal/status card,
     - two compact capability cards.
   - Bottom row:
     - provider/model pills such as OpenAI, Claude, Gemini, Codex, More.
   - Bottom footer strip:
     - compact system copyright and protected New API / QuantumNous project attribution.

2. No additional default landing sections below the first viewport.

Authenticated CTA behavior:

- Authenticated users see `Go to Dashboard` and `Docs`.
- Anonymous users see `Get Started`, `View Pricing`, and `Docs`.

## Visual System

Light mode:

- Mostly white/near-white canvas.
- Subtle grid texture is allowed.
- Restrained blue/cyan with a small warm accent is allowed.
- No heavy gradients, bokeh, decorative orb fields, or remote imagery.

Dark mode:

- Use token-based colors so the single-screen layout remains readable.
- Globe labels, terminal card, and capability cards must not create black-on-white or white-on-white mismatches.

Typography:

- Use existing app font tokens.
- H1 may be large, but all cards/pills use compact operational sizing.
- Do not scale fonts with viewport width.
- Letter spacing must be `0`, except existing global theme rules outside this feature.

Layout:

- Desktop: copy and action area on the left, globe/terminal/cards on the right, provider pills near the bottom.
- Mobile: content stacks in one column and may scroll slightly if necessary, but it should still feel like one page rather than multiple landing sections.
- Text must not overlap header, cards, pills, or footer.
- No cards inside cards.

## Component Boundaries

Primary files:

- `features/home/index.tsx` keeps loading/custom-content behavior and renders only the single-screen default home.
- `features/home/components/sections/hero.tsx` owns the complete default single-screen page.
- `features/home/components/hero-globe.tsx` owns the local globe/network visual.

Existing `Stats`, `Features`, `HowItWorks`, and `CTA` files can remain in the repository but should not be rendered by the default public home in this iteration.

Do not change public route contracts.

## Data and i18n

- Text should use `useTranslation()` and `t('English source key')`.
- Add all new keys to `web/default/src/i18n/locales/{en,zh,fr,ja,ru,vi}.json`.
- Provide real Chinese translations for `zh`.
- Other locales should get reasonable initial translations, not empty strings or untranslated placeholders.
- No backend data dependency is required.

## Error and Empty States

- Keep the existing loading state for custom home page content.
- Keep iframe/markdown custom home page behavior unchanged.
- External docs links keep `target="_blank" rel="noopener noreferrer"`.

## Testing and Verification

Frontend checks:

- `bun run typecheck`
- `bun run i18n:sync`
- `bun run build`

Visual QA:

- Start the frontend dev server.
- Capture desktop and mobile screenshots for `/`.
- Capture dark mode screenshot.
- Verify:
  - no white screen,
  - the page is single-screen on desktop,
  - the globe is visible and framed,
  - header remains usable,
  - CTA buttons fit,
  - provider pills and footer fit,
  - protected project attribution remains present,
  - there are no text overlaps.

## Acceptance Criteria

- Default public home is a compact single-screen page, not a long scrolling landing page.
- Globe/network visual is the main visual anchor.
- Authenticated and unauthenticated CTA logic remains correct.
- Custom home page override still works.
- Dashboard, login/register, backend, and pricing internals are untouched unless required by type errors.
- Protected New API / QuantumNous attribution is preserved.
- All new text is translated across supported frontend locales.
- Build and typecheck pass.
