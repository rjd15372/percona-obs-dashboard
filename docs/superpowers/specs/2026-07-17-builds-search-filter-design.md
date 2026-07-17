# Builds tab: package search filter

**Date:** 2026-07-17
**Status:** Approved

## Problem

The builds tab lists every active package as cards plus a green strip of
fully-built packages. Finding one package means scanning the whole
board. The operator wants a search box on the "Active packages" header
line, right-aligned, that live-filters the package cards by name
substring.

## Decision summary

- **Scope: cards + green strip** — the failing-card grid and the green
  strip's chips both filter, so searching for a healthy package shows it
  green instead of showing nothing. (Rejected: cards only.)
- **No-match state**: a muted one-liner `No packages match "query"`. The
  existing "All packages green" / "No packages found" blocks render only
  while the query is empty. (Rejected: reusing those blocks mid-search —
  "All green" while filtering is misleading.)
- **State lives locally in `FailureBoard.vue`** — the component already
  receives the full package list and derives the failing/ok split; no
  parent or child component changes. (Rejected: hoisting to MainGrid —
  indirection with no second consumer; a global context-bar search —
  scope creep.)
- Match: case-insensitive substring on the package **name** only (not
  the project). Mockup approved via the visual companion (three states:
  idle, typing, no matches).

## Design

All changes in `frontend/src/components/FailureBoard.vue`.

### Search box

In the section-header row, after the count span, right-aligned via
`ml-auto`:

- `<input type="search">` bound `v-model.trim="query"`, placeholder
  `filter packages…`, `aria-label="Filter packages by name"`.
- `@keydown.escape` clears the query (native search inputs also render
  their own clear-×).
- Styling from existing tokens: `bg-bg-card` on the panel background,
  `border border-border rounded-[8px]`, `text-[12.5px]`, padding
  `px-[10px] py-[5px]`, width `w-[200px]`, `text-text-primary` with
  muted placeholder.

### Filtering

```ts
const query = ref('')
const visiblePackages = computed(() => {
  const q = query.value.toLowerCase()
  if (!q) return props.packages
  return props.packages.filter((p) => p.name.toLowerCase().includes(q))
})
```

`failingPackages` and `okPackages` derive from `visiblePackages` instead
of `props.packages`, so the card grid and `GreenStrip` filter without
touching either child component. `attentionCount` keeps meaning "failing
cards shown".

### Header count line

- Query empty (today's text, unchanged):
  `17 packages · sorted by severity`
- Query active: `3 of 17 packages · matching "pg"` — the first number is
  visible failing cards, the second the unfiltered failing count.

### Empty / green / no-match states

- `query === ''`: existing behavior exactly — "All packages green" when
  no failing cards but packages exist, "No packages found" when the list
  is empty.
- `query !== ''` and `visiblePackages` is empty: muted centered
  one-liner `No packages match "pg"` (`text-text-muted`, `text-[13px]`,
  same vertical padding as the existing empty state). The green/empty
  blocks are suppressed while a query is active.
- `query !== ''` with matches: cards and strip render the filtered
  lists; the green strip keeps its own "All clear · N packages fully
  built" label semantics (N = filtered ok count, which its existing
  `packages.length` binding already produces).

## Error handling

None new — pure client-side derivation over props already in hand. The
`v-model.trim` guard means whitespace-only input behaves as an empty
query.

## Testing

- `npm run build` (vue-tsc + vite) exits 0.
- Visual: typing narrows cards and chips live; count line switches to
  "N of M · matching"; Escape and the native × clear back to the full
  board; a query matching only green packages shows the strip with no
  cards and no "All green" banner; a nonsense query shows the no-match
  notice.

## Alternatives considered

- Filter in `MainGrid` and pass down — rejected (no second consumer).
- Global search in the context bar — rejected (multi-tab scope creep).
- Matching project names too — rejected; the request is package-name
  substring.
