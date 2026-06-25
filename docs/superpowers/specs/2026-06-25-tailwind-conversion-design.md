# Tailwind Conversion (Stage 1 of Mobile Responsiveness) — Design

**Date:** 2026-06-25
**Status:** Approved (design)

## Context

The frontend goal is to render well on mobile. We split that work into two independent sub-projects, each with its own spec → plan → implementation cycle:

- **Stage 1 (this spec):** Convert all components from inline `style="…"` attributes and scoped-CSS layout to Tailwind utility classes, with **zero intended visual or behavioral change**.
- **Stage 2 (separate, later spec):** Add responsive/mobile reflow on top of the converted codebase. Decisions already made for Stage 2 are recorded in the appendix so they are not lost.

Doing the conversion first, with no behavior change, de-risks both halves: the refactor can be reviewed purely for equivalence, and the later responsive work starts from a clean Tailwind base.

## Problem

The frontend has no consistent styling system. Layout lives in a mix of inline `style="…"` attributes and scoped `<style>` blocks with hardcoded pixel values. Tailwind is configured (`tailwind.config.ts`, with theme color tokens mapped to CSS variables) but its utilities are barely used. Inline styles cannot express media queries or pseudo-states, which blocks the Stage 2 responsive work and makes the codebase harder to maintain.

## Goal

Convert every component's styling to Tailwind utility classes (removing inline styles and migrating scoped layout CSS), preserving the **exact current appearance** in both light and dark themes on desktop. No layout reflow, no responsiveness, no markup or behavior changes.

## Scope

**In scope:** Converting styling to Tailwind in these components (grouped by complexity / suggested order):
- Leaf: `EventRow.vue`, `PackageCard.vue`, `GreenStrip.vue`, `TimeWindowPicker.vue`, `PRBoard.vue`
- Mid: `AppHeader.vue`, `ContextBar.vue`, `HealthHeader.vue`, `ArtifactsVersionBar.vue`, `ArtifactsPanel.vue`, `PackageEventGroup.vue`
- Complex: `MainGrid.vue`, `FailureBoard.vue`, `EventLog.vue`, `PackagesSubTab.vue`, `ContainersSubTab.vue`
- Root: `App.vue`

Optionally, recurring off-scale values may be added to `tailwind.config.ts` `theme.extend` as named tokens.

**Out of scope (explicitly):**
- Any responsive utilities or breakpoints (that is Stage 2).
- Any change to layout, spacing, colors, or behavior — output must be visually identical.
- Markup/structure changes, refactoring of component logic, renaming.
- `theme.css` token values (the dark-mode palette is final; only its *consumption* may shift from CSS-var inline styles to Tailwind color utilities that resolve to the same vars).

## Architecture

Tailwind is already wired up: `tailwind.config.ts` maps the theme CSS variables to color utilities (`bg-app`, `bg-card`, `text-primary`, `text-secondary`, `border`, `ok`, `fail`, `warn`, `brand-purple`, etc.), and `theme.css` defines the light/dark values. The conversion therefore reuses the existing token system — a class like `bg-bg-card text-text-primary border border-border` resolves to the same CSS variables the inline styles use today, so dark mode keeps working unchanged.

Each component is an isolated unit: converting one does not affect the others (scoped styles and inline styles are component-local). This makes the work cleanly parallelizable and independently verifiable, one component per commit (or one small batch of leaf components per commit).

## Conversion Conventions

**Value fidelity (no visual drift):**
- Where an existing pixel value matches Tailwind's default scale, use the standard utility (8px→`gap-2`, 12px→`p-3`, 16px→`gap-4`/`p-4`, 24px→`p-6`).
- Where the value is off-scale, use Tailwind **arbitrary values** to preserve it exactly: `text-[13px]`, `rounded-[14px]`, `gap-[18px]`, `px-[11px]`, `shadow-[0_1px_2px_rgba(0,0,0,0.12)]`, `max-w-[1360px]`, `w-[440px]`, `w-[220px]`, etc.
- Colors use the mapped theme tokens (`bg-bg-app`, `text-text-muted`, `border-border-strong`, `text-ok`, etc.) — never re-hardcode hex that duplicates a token.
- A recurring off-scale value MAY be promoted to a named token in `theme.extend` (spacing/borderRadius/fontSize/boxShadow) only if it repeats enough to be worth it; otherwise leave it as an arbitrary value.

**What stays as scoped `<style>`:** keep a minimal scoped block per component only for things Tailwind cannot express cleanly:
- Custom scrollbar styling (`::-webkit-scrollbar*`)
- `@keyframes` and animations
- `::before` / `::after` pseudo-elements
- Genuinely complex descendant/combinator selectors that don't map to utilities

Everything else moves to template classes. Do not force-fit; an awkward 6-class arbitrary-value soup that's genuinely a complex selector may remain scoped CSS.

**States:** `:hover`, `:disabled`, `.active`, etc. become Tailwind variants (`hover:`, `disabled:`, and `group`/`peer` or data-attribute variants where a parent state drives a child). The selected-pill `.active` borders and the like must reproduce the same visual states.

## Component Conversion Unit (applies to every component)

Each component conversion task:
1. Replace `style="…"` attributes and scoped-CSS layout rules with equivalent Tailwind utility classes in the template, following the fidelity rules above.
2. Retain a minimal `<style>` block only for the Tailwind-inexpressible cases listed above.
3. Make no structural/markup changes and add no responsive utilities.
4. Verify (see below), then commit (per component, or per small batch for the leaf group).

## Verification

A code-only refactor cannot be visually verified by an automated agent, so the safety net is layered, per component:

1. **Build green:** `cd frontend && npm run build` (runs `vue-tsc && vite build`) passes with no type or build errors.
2. **Declaration-equivalence review:** a reviewer confirms that every original CSS declaration and inline style has an exact Tailwind equivalent in the converted output — same property values, same `:hover`/`:disabled`/`.active` states, same dark-mode behavior. Nothing dropped, nothing added.
3. **Human spot-check:** after each batch, the user eyeballs the affected view in light + dark against the pre-conversion `main` to confirm visual parity. The app can be driven directly or via side-by-side comparison.

Per-component commits keep any regression easy to bisect.

## Risks

- **Subtle drift from snapping to scale.** Mitigated by preferring arbitrary values for any off-scale number rather than rounding to the nearest utility.
- **Pseudo-element / scrollbar / keyframe loss.** Mitigated by explicitly keeping those in scoped CSS.
- **Dark-mode regressions.** Mitigated by reusing the mapped color tokens (which resolve to the same CSS vars) and spot-checking both themes.
- **Class verbosity.** Accepted; optional `theme.extend` tokens can reduce it for repeated values.

## Acceptance Criteria

- [ ] All listed components have their inline `style="…"` attributes removed (except where a dynamic inline style is genuinely required by data binding and has no static Tailwind equivalent — to be flagged in review).
- [ ] Scoped `<style>` blocks retain only Tailwind-inexpressible rules (scrollbars, keyframes, pseudo-elements, complex selectors).
- [ ] `npm run build` passes.
- [ ] Each component passes declaration-equivalence review (no visual/state/dark-mode change).
- [ ] Human spot-check confirms light + dark parity with pre-conversion `main`.
- [ ] No responsive utilities or breakpoints introduced (deferred to Stage 2).

## Appendix — Stage 2 Decisions (out of scope here, recorded for the next spec)

When Stage 2 (responsive mobile) is brainstormed, these decisions are already made:
- **Goal:** Responsive reflow — same features/information on all screens, no horizontal scroll; not a separate mobile redesign.
- **Tiers:** phone (<640px), tablet (640–1024px), desktop (≥1024px). Uses Tailwind `sm`/`lg` breakpoints.
- **Board:** single-column stack on phone/tablet (FailureBoard cards 1-col on phone, 2-col from 640px).
- **Event Log:** collapsible tappable section on phone/tablet (collapsed by default, keeps failures above the fold); side panel on desktop.
- **Packages repo sidebar:** replaced by a full-width grouped dropdown selector at top on phone/tablet; 220px sidebar on desktop.
- **CVE security table:** reflows to stacked per-CVE cards on phones; table on larger screens.
- **Shared infra:** a small `useMediaQuery` composable for the JS-driven behaviors (event-log collapse, repo dropdown).
