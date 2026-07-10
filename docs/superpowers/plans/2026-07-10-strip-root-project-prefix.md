# Strip `isv:percona:` Root Prefix From UI Labels — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** All visible project-name labels in the dashboard drop the constant `isv:percona:` root prefix, while URLs and data logic keep the full name.

**Architecture:** A single display-only helper `shortProject()` in `frontend/src/lib/project.ts` strips the hardcoded prefix at render time. Seven template interpolations across six Vue components wrap their project value with the helper. No API, state, or data-layer changes.

**Tech Stack:** Vue 3 (`<script setup>` SFCs), TypeScript, Vite. Build check via `vue-tsc && vite build`. No frontend test framework exists; verification is the type-checked build plus a visual check in `task dev`.

**User decisions (already made):**
- Strip the literal hardcoded prefix `isv:percona:` — no config plumbing ("Hardcode `isv:percona:`").
- Strip from all visible text; hrefs keep the full project name; no tooltip with the full name ("Strip everywhere, links keep full").
- Approach: shared display helper called at each display site (not data-layer transform, not a component).

Spec: `docs/superpowers/specs/2026-07-10-strip-root-project-prefix-design.md`

---

### Task 1: `shortProject()` helper + apply to all display sites

**Goal:** Create the `shortProject()` helper and wrap the seven visible-text display sites so project labels render without `isv:percona:`.

**Files:**
- Create: `frontend/src/lib/project.ts`
- Modify: `frontend/src/components/PackageCard.vue:229`
- Modify: `frontend/src/components/EventRow.vue:57`
- Modify: `frontend/src/components/PackageEventGroup.vue:91`
- Modify: `frontend/src/components/GreenStrip.vue:56`
- Modify: `frontend/src/components/RebuildBarChart.vue:41,46`
- Modify: `frontend/src/components/CveExposureTable.vue:192`
- Modify: `frontend/src/components/OverviewPanel.vue:113`

**Acceptance Criteria:**
- [ ] `frontend/src/lib/project.ts` exports `shortProject`; `shortProject('isv:percona:ppg:staging')` returns `'ppg:staging'` and `shortProject('other:proj')` returns `'other:proj'` unchanged
- [ ] All seven listed template interpolations render through `shortProject(...)`
- [ ] No `href`, `projectUrl`/`packageUrl` argument, or non-display logic is wrapped — `grep -rn "shortProject" frontend/src` shows only the helper file and the seven display sites (plus imports)
- [ ] `cd frontend && npm run build` exits 0

**Verify:** `cd frontend && npm run build` → `vue-tsc` reports no errors and `vite build` completes with exit code 0

**Steps:**

- [ ] **Step 1: Create the helper**

Create `frontend/src/lib/project.ts`:

```ts
const ROOT = 'isv:percona:'

/** Display-only: strip the constant root project prefix from an OBS project name. */
export function shortProject(name: string): string {
  return name.startsWith(ROOT) ? name.slice(ROOT.length) : name
}
```

- [ ] **Step 2: PackageCard.vue — footer project label**

In `frontend/src/components/PackageCard.vue`, add to the imports in `<script setup>`:

```ts
import { shortProject } from '../lib/project'
```

Then change line 229 from:

```html
      <code class="font-mono text-[10.5px] text-text-muted overflow-hidden text-ellipsis whitespace-nowrap">{{ pkg.project }}</code>
```

to:

```html
      <code class="font-mono text-[10.5px] text-text-muted overflow-hidden text-ellipsis whitespace-nowrap">{{ shortProject(pkg.project) }}</code>
```

- [ ] **Step 3: EventRow.vue — event log project label**

In `frontend/src/components/EventRow.vue`, add the import to `<script setup>`:

```ts
import { shortProject } from '../lib/project'
```

Change line 57 from:

```html
        <code class="font-mono text-[10px] text-text-muted">{{ props.event.project }}</code>
```

to:

```html
        <code class="font-mono text-[10px] text-text-muted">{{ shortProject(props.event.project) }}</code>
```

- [ ] **Step 4: PackageEventGroup.vue — group header project label**

In `frontend/src/components/PackageEventGroup.vue`, add the import to `<script setup>`:

```ts
import { shortProject } from '../lib/project'
```

Change line 91 from:

```html
          <code class="font-mono text-[10px] text-text-muted">{{ project }}</code>
```

to:

```html
          <code class="font-mono text-[10px] text-text-muted">{{ shortProject(project) }}</code>
```

Note: `project` here is a prop (`defineProps<{ project: string; ... }>`), used directly in the template.

- [ ] **Step 5: GreenStrip.vue — project link text (href keeps full name)**

In `frontend/src/components/GreenStrip.vue`, add the import to `<script setup>`:

```ts
import { shortProject } from '../lib/project'
```

Change line 56 from:

```html
      >{{ group.project }} ↗</a>
```

to:

```html
      >{{ shortProject(group.project) }} ↗</a>
```

Do NOT change the `:href="projectUrl(group.project)"` on line 52 or `:href="packageUrl(group.project, pkg.name)"` on line 62 — links need the full project name.

- [ ] **Step 6: RebuildBarChart.vue — bar label and aria-label**

In `frontend/src/components/RebuildBarChart.vue`, add the import to `<script setup>`:

```ts
import { shortProject } from '../lib/project'
```

Change line 41 from:

```html
            <span class="font-mono text-[12.5px] font-semibold truncate">{{ bar.project }}</span>
```

to:

```html
            <span class="font-mono text-[12.5px] font-semibold truncate">{{ shortProject(bar.project) }}</span>
```

Change line 46 from:

```html
            :aria-label="`${bar.project} — ${bar.count} rebuilds`"
```

to:

```html
            :aria-label="`${shortProject(bar.project)} — ${bar.count} rebuilds`"
```

Do NOT change `accentOf(bar.project)` calls (lines 40, 50) or the `:key="bar.project"` — those are logic, not display text.

- [ ] **Step 7: CveExposureTable.vue — project row label**

In `frontend/src/components/CveExposureTable.vue`, add the import to `<script setup>`:

```ts
import { shortProject } from '../lib/project'
```

Change line 192 from:

```html
          <span class="font-mono text-[13.5px] font-semibold truncate">{{ p.project }}</span>
```

to:

```html
          <span class="font-mono text-[13.5px] font-semibold truncate">{{ shortProject(p.project) }}</span>
```

Do NOT change `aggregate(p.project)`, `expanded[p.project]`, `regionId(p.project)`, `toggle(p.project)`, `accentOf(p.project)`, or `imageSuffix(p.project, img)` — those key into data structures by full name.

- [ ] **Step 8: OverviewPanel.vue — Most Rebuilt stat footnote**

In `frontend/src/components/OverviewPanel.vue`, add the import to `<script setup>`:

```ts
import { shortProject } from '../lib/project'
```

Change line 113 from:

```html
              <b class="text-text-secondary">{{ topPackage.count }}</b> rebuilds · <span class="font-mono text-[10.5px]">{{ topPackage.project }}</span>
```

to:

```html
              <b class="text-text-secondary">{{ topPackage.count }}</b> rebuilds · <span class="font-mono text-[10.5px]">{{ shortProject(topPackage.project) }}</span>
```

- [ ] **Step 9: Build check**

Run: `cd frontend && npm run build`
Expected: `vue-tsc` reports no type errors; `vite build` completes; exit code 0.

If `npm run build` fails because local `node_modules` is stale, run `npm ci` first and retry.

- [ ] **Step 10: Scope check**

Run: `grep -rn "shortProject" frontend/src`
Expected: matches only in `lib/project.ts` (definition), and in the six components — one import line each plus the seven call sites listed above. No matches inside `:href`, `projectUrl(`, `packageUrl(`, `accentOf(`, `aggregate(`, or other logic calls.

- [ ] **Step 11: Visual check**

Run: `task dev` (from the repo root) and open http://localhost:4000.
Expected:
- Board package cards show e.g. `ppg:staging` instead of `isv:percona:ppg:staging`
- Event log rows and grouped events show short names
- Overview tab: "Rebuilds by project" bars, "CVE exposure by project" rows, and the "Most Rebuilt" footnote show short names
- All-clear strip (if visible) shows the short name as link text, and clicking it still opens the correct `build.opensuse.org/project/show/isv:percona:...` page
- PR board subproject labels are unchanged (already short)

Then stop the stack with `task down` if it wasn't already running.

- [ ] **Step 12: Commit**

```bash
git add frontend/src/lib/project.ts frontend/src/components/PackageCard.vue frontend/src/components/EventRow.vue frontend/src/components/PackageEventGroup.vue frontend/src/components/GreenStrip.vue frontend/src/components/RebuildBarChart.vue frontend/src/components/CveExposureTable.vue frontend/src/components/OverviewPanel.vue
git commit -s -m "feat(ui): strip isv:percona: root prefix from project labels"
```

Note: this repo requires `git commit -s` (signed-off-by) and no `Co-Authored-By` trailer.
