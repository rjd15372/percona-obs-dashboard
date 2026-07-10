# Strip `isv:percona:` root prefix from displayed project names

**Date:** 2026-07-10
**Status:** Approved

## Problem

Every project name shown in the dashboard UI carries the root project prefix
`isv:percona:`, which is identical for all projects and adds noise to labels
(e.g. `isv:percona:ppg:staging` where `ppg:staging` says everything).

## Decision summary

- Strip the literal, hardcoded prefix `isv:percona:` — no config plumbing.
  This matches the existing frontend pattern (`contexts.ts` and `App.vue`
  already hardcode `isv:percona`).
- Strip from all visible text labels. Hrefs to build.opensuse.org keep the
  full project name. No tooltip with the full name is added.
- Display-only transform via a shared helper; underlying data is untouched.

## Design

### Helper

New file `frontend/src/lib/project.ts`:

```ts
const ROOT = 'isv:percona:'

export function shortProject(name: string): string {
  return name.startsWith(ROOT) ? name.slice(ROOT.length) : name
}
```

A string without the prefix passes through unchanged, so the helper is safe
for any input, including already-short names.

### Display sites (7 edits, 6 components)

Wrap only the visible-text interpolations with `shortProject(...)`:

| File | Site |
|---|---|
| `frontend/src/components/PackageCard.vue:229` | `{{ pkg.project }}` footer label |
| `frontend/src/components/EventRow.vue:57` | `{{ event.project }}` in event log rows |
| `frontend/src/components/PackageEventGroup.vue:91` | `{{ project }}` group header |
| `frontend/src/components/GreenStrip.vue:56` | `{{ group.project }} ↗` link text (href keeps full name) |
| `frontend/src/components/RebuildBarChart.vue:41,46` | bar label and its `aria-label` (kept consistent for screen readers) |
| `frontend/src/components/CveExposureTable.vue:192` | project row label |
| `frontend/src/components/OverviewPanel.vue:113` | `{{ topPackage.project }}` top-package stat |

### Untouched

- All hrefs to build.opensuse.org (full project name required for the URL).
- Prefix parsing in `frontend/src/composables/useRealtimeStream.ts`.
- Context definitions in `frontend/src/lib/contexts.ts`.
- `frontend/src/components/PRBoard.vue` — its `subprojectLabel()` already
  produces a short label.
- PR-branch projects rendered by generic labels become e.g. `PR:pr-42:ppg17`:
  root stripped, PR context kept.

### Data flow and error handling

No API or state changes; the transform happens at render time only. The only
edge case is a non-prefixed string, which passes through unchanged.

## Verification

The frontend has no automated test setup, so verification is visual:

1. `task dev`, open http://localhost:4000.
2. Confirm each of the six components shows project names without
   `isv:percona:`.
3. Confirm the GreenStrip project link still opens the correct
   build.opensuse.org project page.
4. Confirm the PR board view still renders its short subproject labels.

## Alternatives considered

- **Strip at the data layer** (transform API responses in composables):
  rejected — consumers needing the full name (OBS links, prefix matching,
  PRBoard parsing) would break or need the prefix re-added.
- **`<ProjectName>` component**: rejected — heavier than needed for a pure
  text transform.
- **Derive prefix from backend `obs_root` config**: rejected — requires API
  plumbing for a value the frontend already hardcodes elsewhere.
