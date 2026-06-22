# URL State / Shareable Links — Design Spec

**Date:** 2026-06-22
**Status:** Approved

## Goal

Make every meaningful view of the dashboard bookmarkable and shareable via URL.
A user should be able to copy the browser URL at any point and send it to a
colleague who will land on the exact same tab, context, version, filters, and
sub-tab.

---

## Approach

A thin `useUrlState` composable (no new dependencies) that provides bidirectional
sync between Vue reactive refs and `window.location.search`. URL updates use
`history.replaceState` — no new history entries, so the browser Back button
leaves the dashboard entirely rather than stepping through internal navigation.

---

## URL Schema

Query-param based. Params at their default value are omitted to keep URLs clean.

| Param | Values | Default when absent |
|---|---|---|
| `tab` | `board` \| `artifacts` | `board` |
| `ctx` | `ppg` \| `pr-106` \| `pr-92` … | `ppg` |
| `version` | `14` \| `17` … | `""` (All versions) |
| `tags` | comma-separated: `rpm,deb` | `[]` (no filters) |
| `actx` | `ppg` \| `releases` \| `pr-106` … | `ppg` |
| `aversion` | `17` … | latest available for `actx` (data-driven) |
| `sub` | `packages` \| `containers` | `packages` |

### Example URLs

```
/?tab=board&ctx=pr-106
/?tab=board&ctx=ppg&version=14&tags=rpm,deb
/?tab=artifacts&actx=releases&aversion=17&sub=containers
/?tab=artifacts&actx=pr-106&sub=packages
```

### Context key derivation

Context objects are identified by a short key derived from their OBS prefix:

| Prefix | Key |
|---|---|
| `isv:percona:ppg` | `ppg` |
| `isv:percona:ppg:releases` | `releases` |
| `isv:percona:PR:pr-106:ppg` | `pr-106` |

Rule: if the prefix contains the segment `PR` (case-insensitive), extract the
`pr-NNN` segment. Otherwise use the last colon-delimited segment.

---

## State Lifting

`ArtifactsPanel.vue` currently owns three private refs that need URL
representation:

- `localVersion` → lifted to `App.vue` as `artifactsVersion`
- `artifactsTab` → lifted to `App.vue`
- `selectedContext` (artifacts) → lifted to `App.vue` as `artifactsContext`

After the lift, `ArtifactsPanel` receives these as props and emits changes back
up. `App.vue` becomes the single source of truth for all shareable state.

### Complete shareable state in App.vue

| Ref | URL param |
|---|---|
| `mainTab` | `tab` |
| `selectedContext` (board) | `ctx` |
| `version` (board) | `version` |
| `activeTags` | `tags` |
| `artifactsContext` | `actx` |
| `artifactsVersion` | `aversion` |
| `artifactsTab` | `sub` |

---

## `useUrlState` Composable

**Location:** `frontend/src/composables/useUrlState.ts`

### Signature

```ts
function useUrlState(state: {
  mainTab: Ref<'board' | 'artifacts'>
  boardCtx: Ref<Context>             // resolved board context (written by composable on load)
  version: Ref<string>
  activeTags: Ref<string[]>
  artifactsCtx: Ref<Context>         // resolved artifacts context (written by composable on load)
  artifactsVersion: Ref<string>
  artifactsTab: Ref<'packages' | 'containers'>
  boardContexts: Ref<Context[]>      // used to resolve ctx key → Context object
  artifactsContexts: Ref<Context[]>  // used to resolve actx key → Context object
}): void
```

### Behaviour

**On mount** — reads `window.location.search`, parses params, hydrates refs:
- String params (`tab`, `version`, `aversion`, `sub`) set directly on their ref.
- `tags` split on `,` into `activeTags`.
- `ctx` and `actx` parsed as raw string keys; resolved to `Context` objects via
  `keyToContext` immediately if the context lists are already populated, otherwise
  stored internally and resolved once the watcher fires.

**Write-back** — a `watchEffect` watches all state refs and calls
`history.replaceState` with the serialised params. Params at default value are
omitted.

**Context resolution** — two `watch` calls on `boardContexts` and
`artifactsContexts`. When a list becomes non-empty and a pending key exists, the
matching `Context` is resolved and the key cleared. If no match is found (e.g.
a PR that no longer exists), the default context (`ppg`) is used as fallback.

**`aversion` default** — when `aversion` is absent from the URL, the ref starts
as `""`. A `watch` on `artifactsContexts` / available versions snaps it to
`versions[0]` (latest) once data loads, but only if the user has not explicitly
set a version.

### Helper functions (pure, exported for testing)

```ts
// Derives the URL key from a Context object's prefix
function contextToKey(ctx: Context): string

// Finds a Context by key in a list; returns undefined if not found
function keyToContext(key: string, contexts: Context[]): Context | undefined
```

### Usage in App.vue

```ts
useUrlState({
  mainTab,
  boardCtx: selectedContext,
  version,
  activeTags,
  artifactsCtx: artifactsContext,
  artifactsVersion,
  artifactsTab,
  boardContexts: contexts,
  artifactsContexts,
})
```

---

## Late-loading PR Contexts

PR contexts are derived from `prGroups` which arrives asynchronously. If a URL
specifies `ctx=pr-106` but `prGroups` hasn't loaded yet:

1. The raw key `"pr-106"` is stored in `boardCtxKey`.
2. The board falls back to the default PPG context temporarily (no flash shown
   to the user since data is still loading anyway).
3. Once `boardContexts` populates, the watcher resolves `"pr-106"` to its
   `Context` object and sets `selectedContext`.

---

## Files Changed

| File | Change |
|---|---|
| `frontend/src/composables/useUrlState.ts` | New — the URL sync composable |
| `frontend/src/App.vue` | Add `artifactsContext`, `artifactsVersion`, `artifactsTab` refs; call `useUrlState` |
| `frontend/src/components/ArtifactsPanel.vue` | Receive lifted state as props; emit changes up |

---

## Out of Scope

- Browser history navigation (Back steps through views) — `replaceState` only.
- Event log time window (`windowMin`, `customFrom`, `customTo`) — not included;
  custom time ranges are transient and not useful to share.
- Theme preference — not shareable state.
