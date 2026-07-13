# Overview Stat-Card Category Breakdowns Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Total Rebuilds and Open CVEs overview cards each gain a mini segmented bar + legend showing per-category breakdowns (rebuilds: devel/staging/PRs; CVEs: staging/releases always, devel/PRs when non-zero).

**Architecture:** Four new theme-level CSS variables carry validated category colors per theme; a new shared `CategoryBreakdown.vue` renders the bar + legend from a `segments` prop; `useOverviewData` gains two per-category computeds built on the existing `categoryOf()`; `OverviewPanel.vue` assembles segments and drops the component into the two cards via a new default slot in `StatCard.vue`.

**Tech Stack:** Vue 3 `<script setup>` SFCs, TypeScript, Tailwind (existing token classes), CSS variables for theming. No test framework exists — verification is the type-checked `npm run build` plus visual checks in both themes.

**User decisions (already made):**
- Breakdown style: "C" — mini segmented bar + legend (over footnote text line and neutral chip row).
- Rebuilds breakdown: "Strictly devel + staging + PRs" — Releases stays in the total, gets no line; visible non-summing accepted.
- CVE breakdown: "Others exist — show when non-zero" — staging + releases always listed; devel/PRs lines only when non-zero.
- CVE counts are combined open (critical+high) per category; severity split stays in the existing Crit/High chips.
- Palette (validated, do not re-litigate — see spec): light `#6E3FF3/#2A78D4/#1F9D55/#E08A00`, dark `#7E55F5/#2A78D4/#1F9D55/#CB7D0F` for Devel/Staging/Releases/PRs.

Spec: `docs/superpowers/specs/2026-07-13-overview-card-breakdowns-design.md`

**Conventions:** commands run from `/home/rdias/Work/percona-obs-dashboard/frontend`. Commits: `git commit -s`, never a `Co-Authored-By:` trailer.

---

### Task 1: Category theme variables + `CategoryBreakdown.vue` + StatCard default slot

**Goal:** The presentation infrastructure: validated category colors as theme variables, the reusable bar+legend component, and a default slot in `StatCard` so cards can host content between value and footnote.

**Files:**
- Modify: `frontend/src/assets/theme.css:35,69` (insert after each theme's `--tint-postgres` line)
- Modify: `frontend/src/components/StatCard.vue:24-25`
- Create: `frontend/src/components/CategoryBreakdown.vue`

**Acceptance Criteria:**
- [ ] `:root` defines `--cat-devel: #6E3FF3`, `--cat-staging: #2A78D4`, `--cat-releases: #1F9D55`, `--cat-prs: #E08A00`
- [ ] `[data-theme="dark"]` overrides `--cat-devel: #7E55F5` and `--cat-prs: #CB7D0F` (staging/releases values repeated unchanged)
- [ ] `CategoryBreakdown.vue` renders a 6px bar (2px gaps, 4px radius) with slices only for count > 0, hides the bar entirely when the total is 0, and always renders one legend entry per passed segment
- [ ] `StatCard.vue` renders a default slot between the value slot and the footnote
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → `vue-tsc` no errors, `vite build` exit 0

**Steps:**

- [ ] **Step 1: Add theme variables**

In `frontend/src/assets/theme.css`, after line 35 (`  --tint-postgres: rgba(0, 94, 214, 0.10);` in `:root`), insert:

```css
  --cat-devel: #6E3FF3;
  --cat-staging: #2A78D4;
  --cat-releases: #1F9D55;
  --cat-prs: #E08A00;
```

After line 69 (`  --tint-postgres: rgba(63, 175, 203, 0.16);` in `[data-theme="dark"]`), insert:

```css
  --cat-devel: #7E55F5;
  --cat-staging: #2A78D4;
  --cat-releases: #1F9D55;
  --cat-prs: #CB7D0F;
```

- [ ] **Step 2: Add a default slot to StatCard**

In `frontend/src/components/StatCard.vue`, the template currently ends:

```html
    <slot name="value" />
    <div class="text-[12px] text-text-muted">
      <slot name="footnote" />
    </div>
```

Change to:

```html
    <slot name="value" />
    <slot />
    <div class="text-[12px] text-text-muted">
      <slot name="footnote" />
    </div>
```

(An empty default slot renders nothing — the three untouched cards are unaffected.)

- [ ] **Step 3: Create the component**

Create `frontend/src/components/CategoryBreakdown.vue`:

```vue
<script setup lang="ts">
import { computed } from 'vue'

export interface Segment {
  label: string    // display label, e.g. "devel", "staging", "PRs"
  count: number
  colorVar: string // CSS variable reference, e.g. "var(--cat-devel)"
}

const props = defineProps<{ segments: Segment[] }>()

const total = computed(() => props.segments.reduce((s, seg) => s + seg.count, 0))

// Bar slices: only non-zero segments occupy width; zero-count segments keep
// their legend entry but render no slice.
const slices = computed(() =>
  props.segments
    .filter(seg => seg.count > 0)
    .map(seg => ({ ...seg, pct: (seg.count / total.value) * 100 })))
</script>

<template>
  <div class="flex flex-col gap-[4px]">
    <div v-if="total > 0" class="flex h-[6px] rounded-[4px] overflow-hidden gap-[2px]">
      <div
        v-for="s in slices"
        :key="s.label"
        :style="{ width: `${s.pct}%`, background: s.colorVar }"
      />
    </div>
    <div class="flex gap-[10px] flex-wrap text-[10.5px] text-text-muted">
      <span v-for="seg in segments" :key="seg.label" class="inline-flex items-center gap-[4px]">
        <span class="w-[7px] h-[7px] rounded-[2px] inline-block shrink-0" :style="{ background: seg.colorVar }" />
        {{ seg.label }} <b class="font-bold tabular-nums">{{ seg.count }}</b>
      </span>
    </div>
  </div>
</template>
```

- [ ] **Step 4: Build check**

Run: `cd frontend && npm run build`
Expected: `vue-tsc` no errors, `vite build` completes, exit 0. (The component is not yet used anywhere — this task only proves it compiles.)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/assets/theme.css frontend/src/components/StatCard.vue frontend/src/components/CategoryBreakdown.vue
git commit -s -m "feat(ui): category theme colors, CategoryBreakdown component, StatCard default slot"
```

---

### Task 2: Per-category computeds + card wiring

**Goal:** `useOverviewData` exposes rebuild and open-CVE counts per category; the two cards render the breakdown per the user's category rules.

**Files:**
- Modify: `frontend/src/composables/useOverviewData.ts:1-2,84-98` (import, new computeds, return)
- Modify: `frontend/src/components/OverviewPanel.vue:1-42,87-149` (imports, segment computeds, two cards)

**Acceptance Criteria:**
- [ ] `rebuildsByCategory` and `openCvesByCategory` return `Record<ProjectCategory, number>` summed via `categoryOf()`
- [ ] Total Rebuilds card shows a `CategoryBreakdown` with exactly three segments — devel, staging, PRs — always present (zeros included); its footnote becomes `last {win}`
- [ ] Open CVEs card shows staging and releases segments always, devel and PRs only when their count > 0, ordered Devel, Staging, Releases, PRs; footnote unchanged
- [ ] The other three cards are unchanged
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0; then visual check per Step 5

**Steps:**

- [ ] **Step 1: Add the computeds**

In `frontend/src/composables/useOverviewData.ts`, extend the imports (line 2 area):

```ts
import { categoryOf, type ProjectCategory } from '../lib/overview'
```

After the `rebuildBars` computed (ends line 91), add:

```ts
  const emptyByCategory = (): Record<ProjectCategory, number> =>
    ({ Devel: 0, Staging: 0, Releases: 0, PRs: 0 })

  const rebuildsByCategory = computed<Record<ProjectCategory, number>>(() => {
    const out = emptyByCategory()
    for (const p of projects.value) out[categoryOf(p.project)] += p.rebuilds
    return out
  })

  const openCvesByCategory = computed<Record<ProjectCategory, number>>(() => {
    const out = emptyByCategory()
    for (const p of projects.value) {
      const cat = categoryOf(p.project)
      for (const img of p.images) out[cat] += img.critical + img.high
    }
    return out
  })
```

Add both to the return object:

```ts
  return {
    snapshot, loading, error,
    totalRebuilds, rebuildDeltaPct, topPackage, topRepo,
    totalCritical, totalHigh, affectedImageCount, avgFixDays, oldestOpenDays,
    rebuildBars, projects,
    rebuildsByCategory, openCvesByCategory,
  }
```

- [ ] **Step 2: Assemble segments in OverviewPanel**

In `frontend/src/components/OverviewPanel.vue` `<script setup>`:

Add the import:

```ts
import CategoryBreakdown from './CategoryBreakdown.vue'
```

Extend the composable destructuring (lines 24-29) with the two new values:

```ts
const {
  snapshot, loading, error,
  totalRebuilds, rebuildDeltaPct, topPackage, topRepo,
  totalCritical, totalHigh, affectedImageCount, avgFixDays, oldestOpenDays,
  rebuildBars, projects,
  rebuildsByCategory, openCvesByCategory,
} = useOverviewData(win)
```

After the `accentOf` function (line 41), add:

```ts
// Rebuilds: strictly devel/staging/PRs (user decision — Releases stays in
// the total but gets no breakdown line).
const rebuildSegments = computed(() => [
  { label: 'devel', count: rebuildsByCategory.value.Devel, colorVar: 'var(--cat-devel)' },
  { label: 'staging', count: rebuildsByCategory.value.Staging, colorVar: 'var(--cat-staging)' },
  { label: 'PRs', count: rebuildsByCategory.value.PRs, colorVar: 'var(--cat-prs)' },
])

// CVEs: staging + releases always; devel/PRs only when non-zero. Order
// follows CATEGORY_ORDER (Devel, Staging, Releases, PRs).
const cveSegments = computed(() => {
  const c = openCvesByCategory.value
  const segs: { label: string; count: number; colorVar: string }[] = []
  if (c.Devel > 0) segs.push({ label: 'devel', count: c.Devel, colorVar: 'var(--cat-devel)' })
  segs.push({ label: 'staging', count: c.Staging, colorVar: 'var(--cat-staging)' })
  segs.push({ label: 'releases', count: c.Releases, colorVar: 'var(--cat-releases)' })
  if (c.PRs > 0) segs.push({ label: 'PRs', count: c.PRs, colorVar: 'var(--cat-prs)' })
  return segs
})
```

- [ ] **Step 3: Wire the Total Rebuilds card**

In the Total Rebuilds `StatCard` (lines 87-103), add the breakdown as default-slot content after the `#value` template, and simplify the footnote. The card becomes:

```html
        <StatCard label="Total Rebuilds" tint="brand">
          <template #icon>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4"><path d="M21 12a9 9 0 1 1-2.6-6.3"/><polyline points="21 3 21 9 15 9"/></svg>
          </template>
          <template #value>
            <div class="flex items-baseline gap-[10px]">
              <span class="text-[34px] font-extrabold leading-none tracking-[-0.02em] tabular-nums">{{ totalRebuilds }}</span>
              <span
                class="text-[13px] font-bold"
                :class="rebuildDeltaPct >= 0 ? 'text-ok' : 'text-fail'"
              >{{ rebuildDeltaPct >= 0 ? '▲' : '▼' }} {{ Math.abs(rebuildDeltaPct) }}%</span>
            </div>
          </template>
          <CategoryBreakdown :segments="rebuildSegments" />
          <template #footnote>
            last {{ win }}
          </template>
        </StatCard>
```

- [ ] **Step 4: Wire the Open CVEs card**

In the Open CVEs `StatCard` (lines 135-149), add the breakdown; footnote stays. The card becomes:

```html
        <StatCard label="Open CVEs" tint="crit">
          <template #icon>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" class="w-4 h-4"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><line x1="12" y1="8" x2="12" y2="12"/><circle cx="12" cy="15.5" r="0.5"/></svg>
          </template>
          <template #value>
            <div class="flex items-baseline gap-[10px]">
              <span class="text-[34px] font-extrabold leading-none tracking-[-0.02em] tabular-nums">{{ totalCritical + totalHigh }}</span>
              <span class="text-[11.5px] font-bold px-2 py-0.5 rounded-md text-crit bg-crit-tint">{{ totalCritical }} Crit</span>
              <span class="text-[11.5px] font-bold px-2 py-0.5 rounded-md text-high bg-high-tint">{{ totalHigh }} High</span>
            </div>
          </template>
          <CategoryBreakdown :segments="cveSegments" />
          <template #footnote>
            across <b class="text-text-secondary">{{ affectedImageCount }}</b> container images
          </template>
        </StatCard>
```

Do NOT touch the Most Rebuilt, Most Rebuilt Repo, or Avg CVE Fix Time cards.

- [ ] **Step 5: Build + visual check**

Run: `cd frontend && npm run build`
Expected: exit 0.

Visual check (needs a running stack — `task dev` at http://localhost:4000, skip if the port is held by production): Overview tab shows both cards with bar + legend; Rebuilds legend always lists devel/staging/PRs; CVE legend lists staging/releases (+devel/PRs only when non-zero); toggle dark theme and confirm the bar colors shift to the dark palette; other three cards unchanged.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/composables/useOverviewData.ts frontend/src/components/OverviewPanel.vue
git commit -s -m "feat(overview): category breakdowns in Total Rebuilds and Open CVEs cards"
```
