# Public Home Page C-API Style Redesign

## Goal

Redesign only the public default home page (`/`) to borrow the visual structure of `https://c-api.cc/`: clean light-first marketing layout, centered hero, concise service positioning, card-based capability sections, pricing entry cards, FAQ, and final call to action.

This is a public landing-page update, not a dashboard/control-panel redesign.

## Scope

In scope:

- Default home page shown when no custom home page content is configured.
- Public header styling only where it affects the home page presentation.
- Home page section content, spacing, card style, responsive layout, and i18n strings.
- Visual QA across desktop and mobile.

Out of scope:

- Authenticated dashboard layout.
- Login/register pages.
- Backend behavior.
- Pricing route internals.
- Custom home page iframe/markdown override behavior.
- Protected project identity, license headers, package metadata, and existing attribution.

## Reference Traits

The reference page uses:

- Light canvas with restrained blue/cyan accents.
- Fixed transparent header that becomes a compact glass bar when scrolled.
- Hero with a small uppercase badge, large headline, short value statement, two primary CTAs, and a right-side dotted globe/network visual.
- Sections with short eyebrow labels, direct headings, and rounded cards.
- Product/tool cards, ecosystem cards, pricing cards, value cards, FAQ accordion, and footer CTA.
- Mobile layout that stacks cleanly without oversized decorative elements.

We should capture these traits without copying text, brand, or assets.

## Proposed Page Structure

The default home page keeps `PublicLayout` and the custom-content branch in `features/home/index.tsx`.

Default section order:

1. `Hero`
   - Badge: AI API Gateway / multi-upstream aggregation positioning.
   - H1 focused on Wynth API as a unified AI gateway.
   - Short copy describing multi-upstream routing, monitoring, scheduling, and unified API compatibility.
   - CTAs:
     - Authenticated users: `Go to Dashboard`, `Docs`.
     - Anonymous users: `Get Started`, `View Pricing`, `Docs`.
   - Replace the current split terminal-heavy composition with a product hero anchored by a globe/network illustration. The globe is the primary reference element from `c-api.cc`: a light dotted sphere, small provider/client labels, and a few route lines converging toward the gateway. It should be CSS/SVG/HTML-based local UI, not copied from the reference image and not loaded from a remote asset.
   - The existing `HeroTerminalDemo` should not stay as the dominant half-screen element. Remove it from the first viewport or compress any code preview into a small supporting card below the globe.

2. `Open Source / Tools`
   - Two to three cards that explain practical value:
     - Multi-upstream aggregation.
     - Channel monitoring and sync.
     - Priority/weight routing strategy.
   - Cards should be concrete and operational, not generic marketing filler.

3. `Ecosystem`
   - Cards for supported usage surfaces and protocols:
     - OpenAI-compatible API.
     - Anthropic/Claude-compatible flows.
     - Gemini and other model providers.
     - Codex / Claude Code / Cherry Studio style clients where appropriate.
   - Use existing icon libraries when possible.

4. `Pricing Preview`
   - Entry cards that link to `/pricing`.
   - Copy should frame pricing around upstream cost management, channel multipliers, and pay-as-you-go gateway usage.
   - This is not a replacement for the pricing page.
   - This section uses static landing-page copy only. It must not call pricing APIs or introduce backend data dependencies.

5. `Value`
   - Four concise value cards:
     - Lower operational cost through channel selection.
     - Availability monitoring.
     - Strict priority and fallback behavior.
     - Unified management for models, keys, and upstreams.

6. `FAQ`
   - Small accordion-style FAQ for common landing-page questions:
     - Which upstreams are supported?
     - Can I self-host?
     - How are priorities and weights used?
     - Can I use it with existing OpenAI-compatible clients?

7. `CTA`
   - Simple centered closing section with the same main CTA logic as hero.

Existing section mapping:

- `Stats` should be removed or folded into the new value/ecosystem copy. The new page should not keep a standalone animated statistics strip unless the numbers are still meaningful for the redesigned message.
- `Features` becomes the practical tools/capabilities section.
- `HowItWorks` becomes either the value section or is replaced by value cards.
- `CTA` remains as the closing call to action, with updated copy and visual treatment.

## Visual System

Light mode:

- Background remains mostly white or near-white.
- Accent palette should use blue/cyan with small violet secondary accents.
- Cards use subtle borders and shadows, not heavy gradients.
- Use rounded cards, but keep operational pages unaffected.
- `PublicHeader` already has the reference-like scroll-to-glass behavior. Implementation should tune it only if the home page requires minor spacing/token adjustments; do not rebuild the header interaction.

Dark mode:

- Preserve readable dark theme.
- Avoid black-on-white mismatches introduced by home-specific styles.
- Use token-based colors (`background`, `card`, `muted`, `border`, `primary`) rather than hard-coded one-off colors where practical.

Typography:

- Use existing app font tokens.
- Hero headline can be large, but section/card headings must stay proportional and not use hero-scale text.
- No negative tracking unless existing local style already applies it.

Layout:

- Max content width around current `max-w-6xl` / `max-w-7xl`.
- Mobile first. Cards stack cleanly at small widths.
- The globe visual should sit to the right of hero copy on desktop and below the copy on mobile. It must fit within the first viewport and must not overlap nav or CTA text.
- Avoid nested cards.
- Avoid decorative orbs/bokeh backgrounds.

## Component Boundaries

Keep existing high-level modules:

- `features/home/index.tsx`
- `features/home/components/sections/hero.tsx`
- `features/home/components/sections/features.tsx`
- `features/home/components/sections/how-it-works.tsx`
- `features/home/components/sections/stats.tsx`
- `features/home/components/sections/cta.tsx`

Allowed additions:

- New section components if they keep files easier to read, for example:
  - `ecosystem.tsx`
  - `pricing-preview.tsx`
  - `faq.tsx`
- Small reusable presentational components inside `features/home/components`.

Do not change public route contracts. `Home` must still show configured custom home content when present.

## Data and i18n

- Text should use `useTranslation()` and `t('English source key')`.
- Add all new keys to `web/default/src/i18n/locales/{en,zh,fr,ja,ru,vi}.json`.
- Provide real Chinese translations for `zh`. Other locales should get reasonable initial translations, not empty strings or untranslated placeholders.
- Run the existing i18n sync script.

No backend data dependency is required for this iteration.

## Error and Empty States

- Keep the existing loading state for custom home page content.
- Keep iframe/markdown fallback behavior unchanged.
- External docs links should keep the current safe `target="_blank" rel="noopener noreferrer"` behavior.

## Testing and Verification

Frontend checks:

- `bun run typecheck`
- `bun run i18n:sync`
- `bun run build`

Visual QA:

- Start the frontend dev server.
- Capture desktop and mobile screenshots for `/`.
- Verify:
  - no white screen,
  - header remains usable,
  - first viewport clearly communicates the product,
  - cards do not overlap,
  - text fits on mobile,
  - light and dark modes are coherent.

## Acceptance Criteria

- Public default home page visually resembles the reference direction without copying the reference brand.
- Authenticated and unauthenticated CTA logic remains correct.
- Custom home page content override still works.
- Dashboard, login/register, backend, and pricing internals are untouched unless required by type errors.
- All new text is translated across supported frontend locales.
- Build and typecheck pass.
