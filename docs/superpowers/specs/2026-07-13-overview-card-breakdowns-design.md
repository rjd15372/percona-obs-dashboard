# Overview stat-card category breakdowns

**Date:** 2026-07-13
**Status:** Approved

## Problem

The overview's Total Rebuilds and Open CVEs stat cards show only aggregate
numbers. There is no at-a-glance sense of where rebuilds happen (devel vs
staging vs PR projects) or where open CVEs sit (staging vs releases images).

## Decision summary

- Both cards gain a **mini segmented bar + legend** breakdown (mockup option
  C, chosen over a footnote text line and a neutral chip row).
- **Total Rebuilds** breaks down strictly into **Devel, Staging, PRs**.
  Releases rebuilds stay inside the total but get no breakdown line — the
  segments may therefore not visibly sum to the total when releases rebuilds
  exist. This is accepted.
- **Open CVEs** always lists **Staging and Releases**; **Devel and PRs lines
  appear only when non-zero**.
- CVE breakdown counts are **combined open (critical + high)** per category,
  matching the card's headline number; the severity split is already carried
  by the existing Crit/High chips.
- New fixed category colors, validated for both themes (see Palette).

## Palette (validated — do not re-litigate)

Fixed category order: Devel, Staging, Releases, PRs. Validated with the
dataviz skill's `validate_palette.js` (lightness band, chroma floor,
adjacent-pair CVD separation, contrast vs surface):

| Category | Light (`:root`, surface `#FFFFFF`) | Dark (`[data-theme="dark"]`, surface `#1A222E`) |
|---|---|---|
| Devel    | `#6E3FF3` | `#7E55F5` |
| Staging  | `#2A78D4` | `#2A78D4` |
| Releases | `#1F9D55` | `#1F9D55` |
| PRs      | `#E08A00` | `#CB7D0F` |

Results: light — all checks pass; `#E08A00` carries a sub-3:1 contrast WARN
whose required relief is the always-present legend labels. Dark — all four
checks pass outright.

These are *category* colors, distinct in role from the per-project
`PROJECT_ACCENTS` used by RebuildBarChart/CveExposureTable. The same hue may
appear in both with different meanings; this is acceptable because the card
legend always names each segment — identity never rides on color alone.

## Design

### 1. Theme variables — `frontend/src/assets/theme.css`

Add to `:root`:

```css
  --cat-devel: #6E3FF3;
  --cat-staging: #2A78D4;
  --cat-releases: #1F9D55;
  --cat-prs: #E08A00;
```

Add to `[data-theme="dark"]`:

```css
  --cat-devel: #7E55F5;
  --cat-staging: #2A78D4;
  --cat-releases: #1F9D55;
  --cat-prs: #CB7D0F;
```

### 2. New component — `frontend/src/components/CategoryBreakdown.vue`

Props:

```ts
interface Segment {
  label: string    // display label, e.g. "devel", "staging", "PRs"
  count: number
  colorVar: string // CSS variable reference, e.g. "var(--cat-devel)"
}
defineProps<{ segments: Segment[] }>()
```

Rendering (the approved mockup-C visual):

- A 6px-high segmented bar: flex row with 2px `gap`, 4px border-radius on
  the container (overflow hidden). Each segment's width is proportional to
  `count / total`; zero-count segments render no slice.
- If the total is 0, the bar row is hidden entirely; only the legend shows.
- Legend row below the bar: for each segment, a 7px rounded square dot in
  the segment color followed by `label count` in muted 10.5px text
  (`text-text-muted`), counts in tabular figures. Legend entries appear for
  every passed segment, including zero-count ones.
- Text wears text tokens only; color appears only in the bar and dots.

### 3. Data layer — `frontend/src/composables/useOverviewData.ts`

Two new computeds using the existing `categoryOf()` from `lib/overview.ts`:

- `rebuildsByCategory: Record<ProjectCategory, number>` — sums `p.rebuilds`
  per category over `snapshot.projects`.
- `openCvesByCategory: Record<ProjectCategory, number>` — sums
  `image.critical + image.high` per category over each project's images.

Both returned from the composable alongside the existing values.

### 4. Card wiring — `frontend/src/components/OverviewPanel.vue`

- **Total Rebuilds card**: `<CategoryBreakdown>` between value and footnote
  with exactly three segments — Devel, Staging, PRs (in that order), from
  `rebuildsByCategory`. All three legend entries always show (zeros
  included). Footnote becomes `last {win}` (drops "across N projects", per
  the approved mockup).
- **Open CVEs card**: `<CategoryBreakdown>` between value and footnote.
  Segments: Staging and Releases always (zeros included), plus Devel and/or
  PRs appended only when their count > 0. Order follows `CATEGORY_ORDER`
  (Devel, Staging, Releases, PRs) for whichever segments are present.
  Footnote unchanged (`across N container images`).
- The other three cards are untouched.

## Error handling

None needed beyond what the component's zero-total rule covers: all inputs
are already-fetched snapshot data; division by zero is avoided by hiding the
bar when the total is 0.

## Testing

The frontend has no test framework; verification is:

1. `cd frontend && npm run build` — vue-tsc type-checks the new component,
   composable returns, and template wiring; exit 0.
2. Visual check in `task dev`: both cards show the bar + legend in light and
   dark themes; Rebuilds legend always shows devel/staging/PRs; CVE legend
   shows staging/releases (+devel/PRs only when non-zero); other cards
   unchanged.

## Alternatives considered

- **Footnote text line** (option A): least visual weight, but the user chose
  the segmented bar.
- **Neutral chip row** (option B): consistent with existing chips, rejected
  by user preference for the bar.
- **Reusing PROJECT_ACCENTS as category colors directly in JS**: rejected in
  favor of theme CSS variables so dark mode gets its own validated steps
  without JS theme detection.
- **Per-severity split in the CVE breakdown** (e.g. "staging 8C 4H"):
  rejected — the severity split already lives in the Crit/High chips;
  category lines carry combined open counts.
