# Event Log Filter Design

## Goal

Add a collapsible filter panel to the Build Events log that lets users narrow the event list by event type, repository, architecture, and package name — without affecting the broader context/scope/version filters that already exist.

## Architecture

The filter is entirely local to `EventLog.vue`. The component already receives `events: Event[]` pre-filtered by context, scope, and version from `App.vue`. A second `filteredEvents` computed inside `EventLog` applies the panel-local filter on top. No changes are needed to `App.vue`, `useEvents.ts`, or any backend code.

## Filter State

All state lives as `ref`s inside `EventLog.vue`:

| Ref | Type | Default | Meaning |
|-----|------|---------|---------|
| `filterOpen` | `boolean` | `false` | Whether the filter panel is expanded |
| `activeTypes` | `Set<EventType>` | `new Set()` | Selected event types; empty = all |
| `filterRepo` | `string` | `''` | Selected repo; empty = all |
| `filterArch` | `string` | `''` | Selected arch; empty = all |
| `filterPackage` | `string` | `''` | Package name substring; empty = no filter |

`activeFilterCount` is a derived computed: `activeTypes.size + (filterRepo ? 1 : 0) + (filterArch ? 1 : 0) + (filterPackage ? 1 : 0)`.

## Available Options Derivation

`availableRepos` and `availableArches` are derived from `props.events` (the unfiltered prop), not from `filteredEvents`. This ensures the dropdowns always reflect the full window, not just the currently-matched events — avoiding options disappearing as you filter.

```ts
const availableRepos = computed(() =>
  [...new Set(props.events.map(e => e.repo).filter(Boolean))].sort()
)
const availableArches = computed(() =>
  [...new Set(props.events.map(e => e.arch).filter(Boolean))].sort()
)
```

`availableTypes` is similarly derived from `props.events` so only types present in the current window appear as pills.

## Filtering Logic

```ts
const filteredEvents = computed(() =>
  props.events
    .filter(e => activeTypes.value.size === 0 || activeTypes.value.has(e.type))
    .filter(e => filterRepo.value === '' || e.repo === filterRepo.value)
    .filter(e => filterArch.value === '' || e.arch === filterArch.value)
    .filter(e => filterPackage.value === '' ||
      e.what.toLowerCase().includes(filterPackage.value.toLowerCase()))
)
```

Package name matching is case-insensitive substring on `event.what`.

## UI Layout

### Header row

```
Build events   17 in window          ● live
[ 1h ][ 24h ▪ ][ 7d ][ custom ]    ⊟ Filter · 2
```

The Filter button sits right-aligned on the time window row. When `activeFilterCount > 0` it shows `"Filter · N"` with a purple tint; otherwise plain `"Filter"`. Clicking toggles `filterOpen`.

### Expanded panel (appears below time window row, above event list)

**Row 1 — Event type pills** (multi-select, coloured with glyph colour when active, muted when inactive):
```
[ ✓ succeeded ][ ✗ failed ][ ▶ build started ][ ↑ published ][ + created ] …
```
Only types present in `props.events` are rendered. Clicking a pill toggles it in `activeTypes`. Clicking an active pill deselects it.

**Row 2 — Dropdowns + search:**
```
[ Repo ▾ ] [ Arch ▾ ] [ 🔍 package name… ] ✕ clear
```
- Repo dropdown: "All repos" + sorted unique repos from `availableRepos`
- Arch dropdown: "All arches" + sorted unique arches from `availableArches`
- Package input: text field, live substring filter as the user types
- "✕ clear": resets all four filter values and collapses `activeTypes` to empty; only shown when `activeFilterCount > 0`

### Event count

The `"N in window"` counter in the header reflects `filteredEvents.length`, not `props.events.length`, so the user always knows how many events match the current filter.

## Component Changes

**`frontend/src/components/EventLog.vue`** — only file modified:
- Add filter state refs
- Add `availableRepos`, `availableArches`, `availableTypes`, `activeFilterCount`, `filteredEvents` computeds
- Add `toggleType(type)` and `clearFilters()` functions
- Update template: filter toggle button in header; collapsible panel with pills + dropdowns; `filteredEvents` passed to `grouped` instead of `props.events`; event count updated to `filteredEvents.length`

## What Is Not Changing

- `App.vue` — no changes
- `useEvents.ts` — no changes
- Backend — no changes
- The existing scope / version / context filters — unchanged; this filter is additive on top
