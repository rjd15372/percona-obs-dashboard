# Responsive Mobile Reflow (Stage 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every view reflow responsively (phone <640 / tablet 640–1024 / desktop ≥1024) with no horizontal scrolling, keeping the desktop layout visually identical to today.

**Architecture:** Almost entirely Tailwind responsive prefixes (`sm:` = 640px, `lg:` = 1024px) on existing elements. One new `useMediaQuery` composable drives the event-log collapse (a state difference, not just styling). The Packages repo sidebar and the CVE table get a second markup variant toggled by `lg:hidden`/`sm:hidden`; everything else is class-only.

**Tech Stack:** Vue 3 (SFC, `<script setup>`), TypeScript, Tailwind CSS 3, Vite.

**User decisions (already made):**
- Tiers: phone <640 / tablet 640–1024 / desktop ≥1024, via Tailwind `sm`/`lg`. No custom breakpoints.
- Board stacks to one column below `lg`; FailureBoard cards 1-col on phone, 2-col from `sm`.
- Event Log: collapsible (collapsed by default) below `lg`; sticky side panel at `lg`.
- Packages repo sidebar → full-width grouped dropdown below `lg`; 220px sidebar at `lg`.
- CVE table → stacked cards below `sm`; `<table>` at `sm`+.
- AppHeader on phone: compact single row (subtitle hidden, title truncated, icon-only toggle); full header at `sm`+.
- Desktop (≥1024px) must look identical to today.

---

### Task 1: useMediaQuery composable

**Goal:** Add a reactive `useMediaQuery(query)` composable returning a `Ref<boolean>` that tracks whether a media query matches.

**Files:**
- Create: `frontend/src/composables/useMediaQuery.ts`

**Acceptance Criteria:**
- [ ] `useMediaQuery('(min-width: 1024px)')` returns a ref that is `true` when the viewport is ≥1024px and updates on resize across that line.
- [ ] The `change` listener is removed on unmount (no leak).
- [ ] `npm run build` passes.

**Verify:** `cd frontend && npm run build` → `✓ built`, no errors.

**Steps:**

- [ ] **Step 1: Create the composable**

```ts
// frontend/src/composables/useMediaQuery.ts
import { ref, onMounted, onUnmounted, type Ref } from 'vue'

/**
 * Reactive wrapper around window.matchMedia.
 * Returns a ref that tracks whether `query` currently matches.
 */
export function useMediaQuery(query: string): Ref<boolean> {
  const mql = window.matchMedia(query)
  const matches = ref(mql.matches)

  function update(e: MediaQueryListEvent) {
    matches.value = e.matches
  }

  onMounted(() => {
    matches.value = mql.matches
    mql.addEventListener('change', update)
  })
  onUnmounted(() => {
    mql.removeEventListener('change', update)
  })

  return matches
}
```

- [ ] **Step 2: Build**

Run: `cd frontend && npm run build`
Expected: `✓ built`, no type errors.

- [ ] **Step 3: Commit**

```bash
git add frontend/src/composables/useMediaQuery.ts
git commit -s -m "feat(frontend): add useMediaQuery composable"
```

---

### Task 2: Responsive spacing/constraint tweaks

**Goal:** Relax the fixed paddings/constraints that block reflow in `App.vue`, `HealthHeader.vue`, `ContextBar.vue`, and `ArtifactsVersionBar.vue` (CSS-only, desktop unchanged).

**Files:**
- Modify: `frontend/src/App.vue`
- Modify: `frontend/src/components/HealthHeader.vue`
- Modify: `frontend/src/components/ContextBar.vue`
- Modify: `frontend/src/components/ArtifactsVersionBar.vue`

**Acceptance Criteria:**
- [ ] Root horizontal padding is `px-4` on phone and `px-7` at `sm`+.
- [ ] HealthHeader’s left summary column no longer forces 300px on phone (`min-w-0 sm:min-w-[300px]`) and its big gap shrinks on phone.
- [ ] ContextBar and ArtifactsVersionBar gaps shrink on phone (`gap-2 sm:gap-4` / `gap-2 sm:gap-4`); both already wrap.
- [ ] Desktop (≥640px) spacing is unchanged; `npm run build` passes.

**Verify:** `cd frontend && npm run build` → `✓ built`.

**Steps:**

- [ ] **Step 1: App.vue root padding** — change the outer div (currently `class="min-h-screen bg-bg-app pt-6 px-7 pb-[60px]"`) to:

```
class="min-h-screen bg-bg-app pt-6 px-4 sm:px-7 pb-[60px]"
```

- [ ] **Step 2: HealthHeader.vue** — the card container (currently `... flex items-center gap-[30px] flex-wrap`) → replace `gap-[30px]` with `gap-4 sm:gap-[30px]`. The left summary column (currently `flex flex-col gap-2 min-w-[300px] flex-1`) → replace `min-w-[300px]` with `min-w-0 sm:min-w-[300px]`.

- [ ] **Step 3: ContextBar.vue** — the top flex container (currently `flex items-center gap-4 flex-wrap`) → `flex items-center gap-2 sm:gap-4 flex-wrap`.

- [ ] **Step 4: ArtifactsVersionBar.vue** — the `.top-row` flex container (currently `flex items-center gap-4 flex-wrap`) → `flex items-center gap-2 sm:gap-4 flex-wrap`.

- [ ] **Step 5: Build & commit**

```bash
cd frontend && npm run build   # expect ✓ built
git add frontend/src/App.vue frontend/src/components/HealthHeader.vue frontend/src/components/ContextBar.vue frontend/src/components/ArtifactsVersionBar.vue
git commit -s -m "feat(frontend): relax fixed spacing/constraints for mobile reflow"
```

---

### Task 3: AppHeader compact phone layout

**Goal:** Make the header a compact single row on phone — subtitle hidden, title truncated, theme-toggle label hidden — while staying identical at `sm`+.

**Files:**
- Modify: `frontend/src/components/AppHeader.vue`

**Acceptance Criteria:**
- [ ] On phone (<640px) the header is one row that does not overflow 375px: subtitle hidden, title truncates with ellipsis, the theme-toggle text label hidden (dot/glyph remains), tab switcher present.
- [ ] At `sm`+ the header is unchanged from today.
- [ ] `npm run build` passes.

**Verify:** `cd frontend && npm run build` → `✓ built`.

**Steps:**

- [ ] **Step 1: Hide subtitle on phone** — the subtitle span (currently `class="text-[12.5px] text-text-muted"`, text "Failure-first build monitor…") → add `hidden sm:block`:

```
<span class="hidden sm:block text-[12.5px] text-text-muted">Failure-first build monitor across every subproject of a product</span>
```

- [ ] **Step 2: Truncate the title and let it shrink** — the title `<h1>` (currently `m-0 text-[21px] font-bold tracking-[-0.01em] text-text-primary`) → add `truncate text-[17px] sm:text-[21px]`; and its parent name container (`flex items-center gap-3`) → add `min-w-0` so truncation works:

```
<div class="flex items-center gap-3 min-w-0">
  <div class="flex flex-col gap-[1px] min-w-0">
    <h1 class="m-0 truncate text-[17px] sm:text-[21px] font-bold tracking-[-0.01em] text-text-primary">Percona OBS Dashboard</h1>
    <span class="hidden sm:block text-[12.5px] text-text-muted">Failure-first build monitor across every subproject of a product</span>
  </div>
</div>
```

- [ ] **Step 3: Hide the toggle label on phone** — in the theme-toggle button, wrap the text label so only the dot shows on phone. The button currently renders a dot span then the text `{{ theme === 'dark' ? 'Dark' : 'Light' }} mode`. Put the text in a `hidden sm:inline` span:

```
<button
  @click="emit('toggle-theme')"
  class="shrink-0 inline-flex items-center gap-2 px-[14px] py-2 rounded-[10px] border border-border bg-bg-card text-text-secondary [font-family:inherit] text-[13px] font-semibold cursor-pointer"
>
  <span class="w-2 h-2 rounded-full bg-brand-purple"></span>
  <span class="hidden sm:inline">{{ theme === 'dark' ? 'Dark' : 'Light' }} mode</span>
</button>
```

- [ ] **Step 4: Build & commit**

```bash
cd frontend && npm run build   # expect ✓ built
git add frontend/src/components/AppHeader.vue
git commit -s -m "feat(frontend): compact AppHeader on phone"
```

---

### Task 4: Board layout grids (MainGrid + FailureBoard)

**Goal:** Stack the board to one column below `lg`, and make the failing-packages grid 1-col on phone / 2-col from `sm`.

**Files:**
- Modify: `frontend/src/components/MainGrid.vue`
- Modify: `frontend/src/components/FailureBoard.vue`

**Acceptance Criteria:**
- [ ] Below `lg`, MainGrid is a single column (FailureBoard above EventLog); at `lg` it is the `minmax(0,1fr) 440px` two-column grid as today.
- [ ] FailureBoard shows one card per row on phone and two from `sm`.
- [ ] `npm run build` passes.

**Verify:** `cd frontend && npm run build` → `✓ built`.

**Steps:**

- [ ] **Step 1: MainGrid.vue** — the grid container (currently `grid grid-cols-[minmax(0,1fr)_440px] gap-[18px] items-start`) → make the two-column track desktop-only:

```
<div class="grid grid-cols-1 lg:grid-cols-[minmax(0,1fr)_440px] gap-[18px] items-start">
```

- [ ] **Step 2: FailureBoard.vue** — the failing-packages grid (currently `grid grid-cols-[repeat(2,minmax(0,1fr))] gap-[14px]`) → 1-col on phone, 2-col from `sm`:

```
<div v-if="failingPackages.length > 0" class="grid grid-cols-1 sm:grid-cols-[repeat(2,minmax(0,1fr))] gap-[14px]">
```

- [ ] **Step 3: Build & commit**

```bash
cd frontend && npm run build   # expect ✓ built
git add frontend/src/components/MainGrid.vue frontend/src/components/FailureBoard.vue
git commit -s -m "feat(frontend): stack board and FailureBoard grid responsively"
```

---

### Task 5: EventLog collapsible on phone/tablet

**Goal:** Below `lg`, render the event log as a collapsible section (collapsed by default) with a tappable title row; at `lg` keep today’s sticky always-open side panel.

**Files:**
- Modify: `frontend/src/components/EventLog.vue`

**Acceptance Criteria:**
- [ ] At `lg`+ the panel is sticky (`lg:sticky lg:top-4`), height-capped, and always shows its body (no toggle) — identical to today.
- [ ] Below `lg`, the panel is not sticky/height-capped; the body (time-window row, filter panel, and event list) is hidden until the user taps the title row; it starts collapsed.
- [ ] A chevron indicates collapsed/expanded state below `lg` only.
- [ ] `npm run build` passes.

**Verify:** `cd frontend && npm run build` → `✓ built`.

**Steps:**

- [ ] **Step 1: Add state in `<script setup>`** — import the composable and add the collapse state. Add near the top of the script:

```ts
import { useMediaQuery } from '../composables/useMediaQuery'

const isDesktop = useMediaQuery('(min-width: 1024px)')
const expanded = ref(false)            // collapsed by default below lg
const showBody = computed(() => isDesktop.value || expanded.value)
```

(Ensure `ref` and `computed` are imported from `vue` — add them to the existing `vue` import if missing.)

- [ ] **Step 2: Make the root sticky/cap desktop-only** — change the root div (currently `class="sticky top-4 bg-bg-card border border-border rounded-[14px] flex flex-col max-h-[calc(100vh-40px)] overflow-hidden"`) to gate the sticky/height behavior on `lg`:

```
<div class="lg:sticky lg:top-4 bg-bg-card border border-border rounded-[14px] flex flex-col lg:max-h-[calc(100vh-40px)] overflow-hidden">
```

- [ ] **Step 3: Make the title row the toggle (below lg)** — the title row (currently `<div class="flex items-center gap-[9px]">` containing the "Build events" `<h2>`, the count span, and the live span) → make it tappable below `lg` and add a chevron. Replace the opening tag and add a chevron at the end of the row:

```
<div
  class="flex items-center gap-[9px] lg:cursor-default cursor-pointer select-none"
  @click="if (!isDesktop) expanded = !expanded"
>
  <h2 class="m-0 text-[15px] font-bold text-text-primary">Build events</h2>
  <span class="text-[11.5px] text-text-muted [font-family:var(--font-mono)]">
    <template v-if="groupedMode">{{ groupedEvents.length }} packages</template>
    <template v-else-if="activeFilterCount > 0">{{ filteredEvents.length }} of {{ events.length }}</template>
    <template v-else>{{ events.length }}</template>
    in window
  </span>
  <span class="ml-auto inline-flex items-center gap-1.5 text-[11px] text-text-muted">
    <span class="w-1.5 h-1.5 rounded-full bg-ok"></span>live
  </span>
  <span class="lg:hidden text-text-muted text-[11px]" :class="{ 'rotate-90': expanded }">▶</span>
</div>
```

- [ ] **Step 4: Wrap the collapsible body** — everything in the header AFTER the title row (the "Time window + filter toggle row", the "Collapsible filter panel", and — after the header `</div>` — the scrollable event list) must only show when `showBody` is true. Wrap those siblings so they share one `v-show`. Concretely: keep the title row inside the header; immediately after the title row, open `<template v-if="showBody">` is NOT valid for `v-show`, so instead add `v-show="showBody"` to each of the two header sub-rows (the time/filter row `<div class="flex items-center gap-2">` and the filter panel `<div v-if="filterOpen" ...>`), and `v-show="showBody"` to the event-list container that follows the header div. (Three `v-show="showBody"` additions — the time/filter row, the filter panel keeps its own `v-if="filterOpen"` plus add `v-show="showBody"`, and the list wrapper.)

  Note: the filter panel already has `v-if="filterOpen"`; add `v-show="showBody"` alongside it so it never shows while collapsed.

- [ ] **Step 5: Build & commit**

```bash
cd frontend && npm run build   # expect ✓ built
git add frontend/src/components/EventLog.vue
git commit -s -m "feat(frontend): collapsible event log below desktop"
```

---

### Task 6: Packages repo sidebar → dropdown below desktop

**Goal:** Below `lg`, replace the 220px repo sidebar with a full-width grouped `<select>`; at `lg` keep the sidebar. Both drive the same `selectedRepo` via `update:art-repo`.

**Files:**
- Modify: `frontend/src/components/PackagesSubTab.vue`

**Acceptance Criteria:**
- [ ] At `lg`+ the 220px sidebar shows and the dropdown is hidden — identical to today.
- [ ] Below `lg` the sidebar is hidden and a full-width grouped dropdown shows (optgroups RHEL / openSUSE / Ubuntu / Debian / Other, only for non-empty groups), selecting a repo emits `update:art-repo` with the repo’s `obs`.
- [ ] The page content stacks below the dropdown (container `flex-col` below `lg`, `flex-row` at `lg`).
- [ ] `npm run build` passes.

**Verify:** `cd frontend && npm run build` → `✓ built`.

**Steps:**

- [ ] **Step 1: Stack the container** — the root (currently `class="flex gap-4 p-4 h-full min-h-0"`) → `class="flex flex-col lg:flex-row gap-4 p-4 h-full min-h-0"`.

- [ ] **Step 2: Hide the sidebar below lg** — the sidebar div (currently `class="w-[220px] shrink-0 self-start bg-bg-card rounded-xl overflow-hidden flex flex-col"`) → add `hidden lg:flex`:

```
<div class="hidden lg:flex w-[220px] shrink-0 self-start bg-bg-card rounded-xl overflow-hidden flex-col">
```

- [ ] **Step 3: Add the dropdown (below lg only)** — immediately before the sidebar div, add a grouped select bound to the same state:

```
<select
  class="lg:hidden w-full font-mono text-[13px] text-text-secondary bg-bg-card border border-border rounded-lg px-3 py-2 cursor-pointer [appearance:auto]"
  :value="selectedRepo?.obs ?? ''"
  @change="emit('update:art-repo', ($event.target as HTMLSelectElement).value)"
>
  <option value="" disabled>Select a build repo…</option>
  <optgroup v-if="rhelRepos.length > 0" label="RHEL">
    <option v-for="repo in rhelRepos" :key="repo.obs" :value="repo.obs">{{ repo.name }}</option>
  </optgroup>
  <optgroup v-if="opensuseRepos.length > 0" label="openSUSE">
    <option v-for="repo in opensuseRepos" :key="repo.obs" :value="repo.obs">{{ repo.name }}</option>
  </optgroup>
  <optgroup v-if="ubuntuRepos.length > 0" label="Ubuntu">
    <option v-for="repo in ubuntuRepos" :key="repo.obs" :value="repo.obs">{{ repo.name }}</option>
  </optgroup>
  <optgroup v-if="debianRepos.length > 0" label="Debian">
    <option v-for="repo in debianRepos" :key="repo.obs" :value="repo.obs">{{ repo.name }}</option>
  </optgroup>
  <optgroup v-if="otherRepos.length > 0" label="Other">
    <option v-for="repo in otherRepos" :key="repo.obs" :value="repo.obs">{{ repo.name }}</option>
  </optgroup>
</select>
```

(These `rhelRepos`/`opensuseRepos`/`ubuntuRepos`/`debianRepos`/`otherRepos` computeds and the `update:art-repo` emit already exist in the component’s `<script setup>`.)

- [ ] **Step 4: Build & commit**

```bash
cd frontend && npm run build   # expect ✓ built
git add frontend/src/components/PackagesSubTab.vue
git commit -s -m "feat(frontend): packages repo dropdown below desktop"
```

---

### Task 7: ContainersSubTab grid + CVE cards on phone

**Goal:** Make the image grid single-column on phone, and render the CVE findings as stacked cards below `sm` while keeping the `<table>` at `sm`+.

**Files:**
- Modify: `frontend/src/components/ContainersSubTab.vue`

**Acceptance Criteria:**
- [ ] Image grid is one card per row on phone and auto-fill `minmax(340px,1fr)` from `sm`.
- [ ] Below `sm`, each CVE finding renders as a card (CVE id + severity prominent, then package, installed→fixed, title); at `sm`+ the existing table shows.
- [ ] No horizontal scroll from the CVE section on phone.
- [ ] `npm run build` passes.

**Verify:** `cd frontend && npm run build` → `✓ built`.

**Steps:**

- [ ] **Step 1: Image grid** — the grid (currently `class="grid grid-cols-[repeat(auto-fill,minmax(340px,1fr))] gap-4"`) → `class="grid grid-cols-1 sm:grid-cols-[repeat(auto-fill,minmax(340px,1fr))] gap-4"`.

- [ ] **Step 2: Hide the table below sm** — the CVE `<table class="cve-table w-full border-collapse text-[11px]">` is the table at `frontend/src/components/ContainersSubTab.vue` (in the `v-for="scan in image.cveScans"` block). Add `hidden sm:table` to it:

```
<table class="cve-table hidden sm:table w-full border-collapse text-[11px]">
```

- [ ] **Step 3: Add the phone card list** — immediately after the `</table>` (still inside the `v-for="scan in image.cveScans"` block, so it uses the same `scan.findings`), add a card list shown only below `sm`:

```
<div class="sm:hidden flex flex-col gap-2">
  <div
    v-for="f in (scan.findings ?? [])"
    :key="f.id"
    class="border border-border rounded-lg p-2.5 flex flex-col gap-1"
  >
    <div class="flex items-center justify-between gap-2">
      <span class="font-mono text-[11px] font-bold text-text-primary">{{ f.id }}</span>
      <span
        class="text-[10px] font-bold uppercase whitespace-nowrap"
        :class="{ 'sev-critical': f.severity === 'CRITICAL', 'sev-high': f.severity === 'HIGH' }"
      >{{ f.severity }}</span>
    </div>
    <div class="font-mono text-[11px] text-text-secondary">{{ f.pkg }}</div>
    <div class="font-mono text-[11px] text-text-muted">{{ f.installed }} → {{ f.fixed }}</div>
    <div class="text-[11px] text-text-secondary">{{ f.title }}</div>
  </div>
</div>
```

(The `.sev-critical`/`.sev-high` scoped classes already exist and only set color; they apply fine to a `<span>`. The finding fields `f.id`, `f.severity`, `f.pkg`, `f.installed`, `f.fixed`, `f.title` match the existing table bindings.)

- [ ] **Step 4: Build & commit**

```bash
cd frontend && npm run build   # expect ✓ built
git add frontend/src/components/ContainersSubTab.vue
git commit -s -m "feat(frontend): responsive container grid and CVE cards on phone"
```

---

## Self-Review

- **Spec coverage:** Root padding (Task 2) ✓; AppHeader compact (Task 3) ✓; MainGrid stack + FailureBoard cols (Task 4) ✓; HealthHeader/ContextBar/ArtifactsVersionBar tweaks (Task 2) ✓; EventLog collapsible via useMediaQuery (Tasks 1+5) ✓; Packages dropdown (Task 6) ✓; ContainersSubTab grid + CVE cards (Task 7) ✓. All acceptance criteria map to tasks.
- **Placeholder scan:** No TBD/TODO; every class change shows exact before→after; the composable has full code; new markup (select, CVE cards, EventLog toggle) is provided verbatim.
- **Type/naming consistency:** `useMediaQuery` signature matches its usage in Task 5; repo computeds (`rhelRepos` etc.) and `update:art-repo` emit referenced in Task 6 exist in the component; CVE finding fields in Task 7 match the existing table bindings; breakpoints consistent (`sm`=640 for FailureBoard cols + CVE cards + image grid; `lg`=1024 for board stack + event-log mode + repo sidebar).

## Notes for the executor

- Task 5 depends on Task 1 (composable). All other tasks touch disjoint files and may run in parallel.
- Desktop (≥1024px) must remain visually identical — every change is gated behind `sm:`/`lg:` so the largest tier keeps today’s classes.
- After implementation, do the manual responsive check at 375 / 768 / 1280px described in the spec.
