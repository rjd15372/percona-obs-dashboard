# Event Log Package Grouping Design

## Goal

Reduce noise in the build event log by grouping events that belong to the same package into a single collapsible row. A package building 16 targets through start → succeed → publish produces 48 events today; grouped mode collapses these to one row showing the most recent outcome.

## Architecture

Pure client-side change. No backend or API modifications. Grouping is a computed property on the events already fetched by `useEvents`. Two files change or are created:

- **`frontend/src/components/EventLog.vue`** — adds toggle state, `groupedEvents` computed, toggle button, and renders `PackageEventGroup` rows in grouped mode.
- **`frontend/src/components/PackageEventGroup.vue`** — new component, renders one package group (collapsed or expanded).

## Grouping Logic

Computed in `EventLog.vue` from `props.events` (the already scope/version-filtered list from App.vue):

1. Reduce events into a `Map<string, Event[]>` keyed by `project + "/" + package`.
2. Each group's events are sorted newest-first; index 0 is the "most recent event".
3. Groups are sorted by their most recent event's `at` timestamp, newest first.
4. The Today / Yesterday / Earlier bucket for a group is determined by its most recent event's `at`.
5. Groups are rebuilt from scratch whenever `props.events` changes (window change, version change, SSE update). All groups start collapsed after a rebuild.

## Toggle

A `ref<boolean> groupedMode` in `EventLog.vue`, defaulting to `false`. Not persisted across page reloads.

**Toggle button:** added to the right of the existing `Filter` button in the EventLog header control row. Label: `⊞ Group`. Uses the same style as the Filter button — purple tint background + purple border + purple text when active (`groupedMode === true`), neutral border + secondary text colour when inactive.

**Header count:** when `groupedMode` is `false`, shows `"N in window"` as today. When `true`, shows `"N packages in window"` where N is the number of groups.

## `PackageEventGroup.vue`

**Props:**
```ts
{
  project: string
  package: string
  scope: string
  events: Event[]   // sorted newest-first, guaranteed non-empty
}
```

**Internal state:** `expanded: ref<boolean>(false)` passed in as a prop from `EventLog.vue` (see Expand State Management below).

**Collapsed row layout** (left to right):
- Expand arrow `▶` (rotates to `▼` when expanded, 0.15s CSS transition)
- Glyph icon (24×24, rounded, colour-coded by `events[0].type` — same colour map as `EventRow`)
- Package name (bold, truncated)
- Event count badge (`"N events"`, muted background pill)
- Timestamp of `events[0].at` (right-aligned, monospace, muted)

Second line below the package name:
- Most recent event's `what` text (secondary colour, truncated) — this is the subtitle

Third line:
- Scope chip (same style as `EventRow`)
- Project path in monospace muted text

**Expanded state:** header row stays in place; below it, an indented list of child rows — one per event. Each child row contains:
- Smaller glyph (20×20) colour-coded by `event.type`
- Vertical connecting line between glyphs (omitted on the last child)
- Event title (`what` with repo/arch stripped out, same as `EventRow.eventTitle()`)
- Repo/arch detail line in monospace if present
- Timestamp right-aligned

Each child row is an `<a>` linking to `event.url` (same as `EventRow`).

**Expanded group visual treatment:** the group receives a subtle background tint (`var(--bg-card-2)`) and a `1px solid var(--border)` border with `border-radius: 9px` to frame the header + children as a unit.

## Filter Panel Interaction

Active filters (type, repo, arch, package name) operate on the underlying flat event list before grouping. In grouped mode:

- A group is **included** if at least one of its events passes all active filters.
- The group **header always shows the most recent event overall** (index 0), regardless of whether that event passes the current filters. This keeps the header stable.
- The **event count badge** shows the total events in the group, not the filtered subset.

## Expand State Management

Expand state cannot live inside `PackageEventGroup` as a plain `ref`, because the `groupedEvents` computed re-runs on every reactive change to `props.events` — including SSE `unshift()` mutations — and would recreate the group list, losing all expand state.

Instead, `EventLog.vue` maintains a `expandedGroups: ref<Map<string, boolean>>` keyed by `project + "/" + package`. It passes each group's current expanded value as a prop to `PackageEventGroup`, along with a `@toggle` emit handler that updates the map. A separate `watch(props.events, ..., { flush: 'sync' })` watching the **array identity** (not contents) clears `expandedGroups` when the whole array is replaced by a new fetch. SSE mutations (which mutate the existing array in-place rather than replacing it) preserve expand state across live updates.

```ts
// EventLog.vue
const expandedGroups = ref<Map<string, boolean>>(new Map())

watch(() => props.events, (_new, old) => {
  if (_new !== old) expandedGroups.value = new Map()
}, { flush: 'sync' })
```

`PackageEventGroup.vue` props:
```ts
{ ..., expanded: boolean }
```
with emit `'toggle': []`.

## Real-time (SSE)

No special handling required. New events arrive via `events.value.unshift()` in `useRealtimeStream` as today. The `groupedEvents` computed reacts automatically:

- If the new event belongs to an existing package: that group moves to the top of its bucket.
- If the new event is a new package: a new group appears at the top of its bucket.
- Expanded state resets only when `props.events` is replaced (new fetch). SSE mutations to the existing array do not reset expand state.

## What Is Not Changing

- The flat event list behaviour is unchanged when `groupedMode` is `false`.
- No backend, API, or SSE protocol changes.
- `EventRow.vue` is unchanged and continues to be used for both the flat list and the child rows inside expanded groups.
- The filter panel, time window picker, and Today/Yesterday/Earlier bucketing all behave identically to today.
