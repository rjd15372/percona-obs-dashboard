# Builds-Tab Package Search Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A right-aligned search box on the "Active packages" header line live-filters the failing-package cards and the green strip by package-name substring.

**Architecture:** All changes local to `FailureBoard.vue`: a `query` ref, a `visiblePackages` computed that the existing failing/ok splits derive from, a conditional header count line, and a no-match notice replacing the green/empty blocks while a query is active. No parent, child, type, or backend changes.

**Tech Stack:** Vue 3 + TypeScript + Tailwind-style utility classes.

**User decisions (already made):**
- Filter scope: cards + green strip (searching a healthy package shows it green).
- No-match state: muted `No packages match "query"` notice; existing "All packages green" / "No packages found" blocks render only while the query is empty.
- Search box on the header line, right-aligned; match is a case-insensitive substring of the package name only. Mockup approved (idle / typing / no-match states).

Spec: `docs/superpowers/specs/2026-07-17-builds-search-filter-design.md`

**Conventions:** frontend commands from `frontend/`. Commits: `git commit -s`, never Co-Authored-By.

---

### Task 1: search filter in FailureBoard

**Goal:** `FailureBoard.vue` renders the search box and filters cards, strip, count line, and empty states per the spec.

**Files:**
- Modify: `frontend/src/components/FailureBoard.vue` (whole file — complete replacement below)

**Acceptance Criteria:**
- [ ] Header row gains a right-aligned `<input type="search">` with placeholder `filter packages…`, `aria-label="Filter packages by name"`, Escape-to-clear
- [ ] Typing filters both the failing-card grid and the `GreenStrip` chips by case-insensitive name substring
- [ ] Count line: empty query → `17 packages · sorted by severity` (unchanged); active query → `3 of 17 packages · matching "pg"` (visible failing vs unfiltered failing)
- [ ] Query active + zero visible packages → centered muted `No packages match "pg"`; the "All packages green" and "No packages found" blocks never render while a query is active
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Replace the component**

Replace the entire contents of `frontend/src/components/FailureBoard.vue` with:

```vue
<script setup lang="ts">
import { computed, ref } from 'vue'
import PackageCard from './PackageCard.vue'
import GreenStrip from './GreenStrip.vue'
import type { Package } from '../types/api'

const props = defineProps<{ packages: Package[]; spotlightStates: string[] }>()

const query = ref('')

const isFailing = (p: Package) => p.rollup_state !== 'succeeded' && p.rollup_state !== 'published'

// Case-insensitive substring filter on the package name; an empty query
// passes everything through.
const visiblePackages = computed(() => {
  const q = query.value.toLowerCase()
  if (!q) return props.packages
  return props.packages.filter(p => p.name.toLowerCase().includes(q))
})

const failingPackages = computed(() => visiblePackages.value.filter(isFailing))
const okPackages = computed(() => visiblePackages.value.filter(p => !isFailing(p)))
const attentionCount = computed(() => failingPackages.value.length)
const totalFailing = computed(() => props.packages.filter(isFailing).length)
</script>

<template>
  <div class="flex flex-col gap-[14px] min-w-0">
    <!-- Section header -->
    <div class="flex items-center gap-[10px]">
      <h2 class="m-0 text-[15px] font-bold text-text-primary">Active packages</h2>
      <span v-if="query" class="text-[12.5px] text-text-muted">{{ attentionCount }} of {{ totalFailing }} package{{ totalFailing !== 1 ? 's' : '' }} · matching “{{ query }}”</span>
      <span v-else class="text-[12.5px] text-text-muted">{{ attentionCount }} package{{ attentionCount !== 1 ? 's' : '' }} · sorted by severity</span>
      <input
        v-model.trim="query"
        type="search"
        placeholder="filter packages…"
        aria-label="Filter packages by name"
        class="ml-auto w-[200px] bg-bg-card border border-border rounded-[8px] px-[10px] py-[5px] text-[12.5px] text-text-primary placeholder:text-text-muted"
        @keydown.escape="query = ''"
      />
    </div>

    <!-- 2-column failure grid -->
    <div v-if="failingPackages.length > 0" class="grid grid-cols-1 sm:grid-cols-[repeat(2,minmax(0,1fr))] gap-[14px]">
      <PackageCard
        v-for="pkg in failingPackages"
        :key="`${pkg.project}/${pkg.name}`"
        :pkg="pkg"
        :spotlight-states="spotlightStates"
      />
    </div>

    <!-- No-match state (only while searching) -->
    <div v-if="query && visiblePackages.length === 0" class="text-center text-text-muted py-8 text-[13px]">
      No packages match “{{ query }}”
    </div>

    <!-- All green state (only when not searching) -->
    <div v-if="!query && failingPackages.length === 0 && packages.length > 0" class="bg-ok-tint border border-ok rounded-[12px] p-7 flex flex-col items-center gap-2 text-center">
      <span class="text-[26px] text-ok font-extrabold">✓</span>
      <span class="text-[15px] font-bold text-text-primary">All packages green</span>
    </div>

    <!-- Empty state (only when not searching) -->
    <div v-if="!query && packages.length === 0" class="text-center text-text-muted py-8 text-sm">
      No packages found
    </div>

    <!-- Green strip -->
    <GreenStrip
      v-if="okPackages.length > 0"
      :packages="okPackages"
      :style="spotlightStates.length > 0 ? 'opacity: 0.2; transition: opacity 0.2s' : 'transition: opacity 0.2s'"
    />
  </div>
</template>
```

The diff vs the current file: `ref` import; `query` ref; `isFailing` helper extracted (was inlined twice); `visiblePackages` computed inserted; `failingPackages`/`okPackages` now filter `visiblePackages` instead of `props.packages`; `totalFailing` computed added; the header gains the conditional count span and the input; the no-match block is new; the green/empty blocks gain `!query` guards. The failure grid and `GreenStrip` bindings are untouched.

- [ ] **Step 2: Build check**

Run: `cd frontend && npm run build`
Expected: vue-tsc no errors, exit 0.

- [ ] **Step 3: Visual check (dev server optional)**

If a dev server is already running, verify in the browser: typing narrows cards and chips; count line switches to "N of M · matching"; Escape and the native × clear; a query matching only green packages shows the strip without the "All green" banner; a nonsense query shows the no-match notice. Otherwise rely on the build plus the reviewer's reading — there is no frontend unit-test infra in this repo.

- [ ] **Step 4: Commit**

```bash
git add src/components/FailureBoard.vue
git commit -s -m "feat(builds): search box filters active packages by name"
```
