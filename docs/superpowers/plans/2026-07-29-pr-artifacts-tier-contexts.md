# Per-Tier PR Artifacts Contexts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Artifacts tab shows packages and containers for PR projects again by expanding each PR into one context per `(PR, tier)` with the correct `ppg:<tier>` prefix, so the shared version derivation finds the numeric version.

**Architecture:** Extract the PR-context construction from `App.vue`'s `artifactsContexts` computed into a pure `prArtifactsContexts(groups)` helper in `lib/contexts.ts`. It emits one `Context` per distinct `(pr, tier)` at prefix `isv:percona:PR:<pr>:ppg:<tier>` with `allowedSubprojects:['containers']` — mirroring the devel/staging contexts so the unchanged shared `deriveVersionKeys`/`matchesVersionKey` resolve versions correctly.

**Tech Stack:** Vue 3 + TS.

**User decisions (already made):**
- One Artifacts context per (PR, tier) — `PR #<n> · Staging` / `PR #<n> · Devel` — mirroring the devel/staging contexts. (Rejected: one PR context with tier-aware version keys — invasive to shared version logic; staging-only — hides devel-tier PR projects.)
- Extract the PR-context construction into a pure helper.
- Label format `PR #<n> · <Tier>`; sort PR number descending, staging before devel.

Spec: `docs/superpowers/specs/2026-07-29-pr-artifacts-tier-contexts-design.md`

**Conventions:** frontend commands from `frontend/`. Frontend has no runtime test runner (only `.test-d.ts` type checks via `vue-tsc`); logic is verified by `npm run build` + manual check. Commits: `git commit -s`, never Co-Authored-By.

---

### Task 1: per-tier PR artifacts contexts

**Goal:** `prArtifactsContexts` builds one context per `(PR, tier)` with the tier-deep prefix; `App.vue` uses it.

**Files:**
- Modify: `frontend/src/lib/contexts.ts` (add `PRGroup` import + `prArtifactsContexts` + `PR_TIERS`)
- Modify: `frontend/src/App.vue` (import the helper; replace the inline PR loop in `artifactsContexts`)
- Create: `frontend/src/lib/contexts.test-d.ts` (pin the helper's signature/return shape)

**Acceptance Criteria:**
- [ ] `prArtifactsContexts` emits, for `isv:percona:PR:pr-4:ppg:staging:18`, a context `{label:'PR #4 · Staging', apiBase:'/api/pr/pr-4', prefix:'isv:percona:PR:pr-4:ppg:staging', allowedSubprojects:['containers']}`; a PR with both `ppg:staging:17` and `ppg:devel:19` yields two contexts
- [ ] Non-tier PR projects (`…:ppg:common:deps`, `…:common:deps:build`) yield no context
- [ ] Contexts sort PR-number descending, staging before devel within a PR
- [ ] `App.vue`'s `artifactsContexts` returns `[devel, staging, releases, ...prArtifactsContexts(prGroups.value)]` with the old inline PR loop removed
- [ ] `npm run build` exits 0

**Verify:** `cd frontend && npm run build` → exit 0

**Steps:**

- [ ] **Step 1: Add the helper to `lib/contexts.ts`**

Change the import line at the top of `frontend/src/lib/contexts.ts` from:

```ts
import type { Context } from '../types/api'
```

to:

```ts
import type { Context, PRGroup } from '../types/api'
```

Append to the end of the file:

```ts
const PR_TIERS = ['staging', 'devel'] as const

// prArtifactsContexts expands PR package groups into one Artifacts context per
// (PR, tier). PR projects follow the devel/staging-restructured layout
// isv:percona:PR:<pr>:ppg:<tier>:<version>[:<sub>], so each tier needs its own
// context whose prefix includes ppg:<tier> — that way the shared
// deriveVersionKeys/matchesVersionKey (which read the version at the prefix
// depth) resolve the numeric version, exactly as they do for the devel/staging
// contexts, and 'containers' folds into the plain version entry. PR projects
// without a versioned ppg:<tier> (e.g. common:deps) contribute no context.
export function prArtifactsContexts(groups: PRGroup[]): Context[] {
  const seen = new Set<string>()
  const contexts: Context[] = []
  for (const group of groups) {
    for (const pkg of group.packages) {
      const parts = pkg.project.split(':')
      const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
      // Expect: …:PR:<pr>:ppg:<tier>:<version>…
      if (prIdx < 0) continue
      const prSegment = parts[prIdx + 1]
      const tier = parts[prIdx + 3]
      const version = parts[prIdx + 4]
      if (!prSegment || parts[prIdx + 2] !== 'ppg') continue
      if (!PR_TIERS.includes(tier as (typeof PR_TIERS)[number])) continue
      if (!version || !/^\d+$/.test(version)) continue
      const key = `${prSegment}:${tier}`
      if (seen.has(key)) continue
      seen.add(key)
      const prNum = prSegment.replace(/^pr-/i, '')
      const tierLabel = tier.charAt(0).toUpperCase() + tier.slice(1)
      contexts.push({
        label: `PR #${prNum} · ${tierLabel}`,
        apiBase: `/api/pr/${prSegment}`,
        prefix: `isv:percona:PR:${prSegment}:ppg:${tier}`,
        allowedSubprojects: ['containers'],
      })
    }
  }
  contexts.sort((a, b) => {
    const na = parseInt(a.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    const nb = parseInt(b.prefix.split(':')[3]?.replace(/^pr-/i, '') ?? '0')
    if (na !== nb) return nb - na // PR number descending
    const ta = a.prefix.split(':')[5] ?? ''
    const tb = b.prefix.split(':')[5] ?? ''
    if (ta === tb) return 0
    return ta === 'staging' ? -1 : 1 // staging before devel
  })
  return contexts
}
```

- [ ] **Step 2: Wire it into `App.vue`**

In `frontend/src/App.vue`, change the contexts import from:

```ts
import { PPG_DEVEL_CONTEXT, PPG_STAGING_CONTEXT, RELEASES_CONTEXT } from './lib/contexts'
```

to:

```ts
import { PPG_DEVEL_CONTEXT, PPG_STAGING_CONTEXT, RELEASES_CONTEXT, prArtifactsContexts } from './lib/contexts'
```

Replace the entire `artifactsContexts` computed (the block from `const artifactsContexts = computed<Context[]>(() => {` through its closing `})`, including the inline `seen`/`prContexts` loop and sort) with:

```ts
// Artifacts contexts: PPG devel/staging + Releases + one context per (PR, tier).
const artifactsContexts = computed<Context[]>(() => [
  PPG_DEVEL_CONTEXT,
  PPG_STAGING_CONTEXT,
  RELEASES_CONTEXT,
  ...prArtifactsContexts(prGroups.value),
])
```

- [ ] **Step 3: Add the type-level test**

Create `frontend/src/lib/contexts.test-d.ts`:

```ts
import { prArtifactsContexts } from './contexts'
import type { Context, PRGroup } from '../types/api'

// Pins the helper signature: PRGroup[] in, Context[] out.
const groups: PRGroup[] = []
const out: Context[] = prArtifactsContexts(groups)
void out
```

- [ ] **Step 4: Build check**

Run: `cd frontend && npm run build`
Expected: vue-tsc no errors (the `.test-d.ts` compiles, `App.vue` uses the helper), exit 0.

Then a manual reasoning pass against the plan's AC using the real prod projects (no runtime runner):
- `isv:percona:PR:pr-4:ppg:staging:18` → parts index of `pr`=3? No — trace it: `['isv','percona','PR','pr-4','ppg','staging','18']`, `prIdx` = index of `PR` (lowercased `pr`) = 2, `prSegment`=`pr-4`, `parts[4]`=`ppg` ✓, `tier`=`parts[5]`=`staging` ✓, `version`=`parts[6]`=`18` ✓ → context `PR #4 · Staging`, prefix `isv:percona:PR:pr-4:ppg:staging`.
- `isv:percona:PR:pr-2:ppg:devel:19` → `PR #2 · Devel`; `…:pr-2:ppg:staging:17` → `PR #2 · Staging` (two contexts).
- `isv:percona:PR:pr-2:ppg:common:deps` → `parts[5]`=`common` ∉ PR_TIERS → skipped. `isv:percona:PR:pr-2:common:deps:build` → `parts[4]`=`common` ≠ `ppg` → skipped.
Confirm these hold by reading the committed helper.

- [ ] **Step 5: Commit**

```bash
git add src/lib/contexts.ts src/lib/contexts.test-d.ts src/App.vue
git commit -s -m "fix(artifacts): per-tier PR contexts so PR packages and containers show"
```
