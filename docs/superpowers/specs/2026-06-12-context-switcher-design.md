# Context Switcher Implementation Design

**Goal:** Replace the separate PR board with a context dropdown in the ContextBar that lets users view any OBS project subtree (PPG or a specific PR) through the same full board UI.

**Architecture:** A context object drives both the API URL and the display. PPG uses the existing `/api/products/` routes; PR contexts use new `/api/pr/{pr}/{subproject}/` routes that insert the `PR:` segment into the OBS prefix. Context discovery runs client-side from the existing `/api/pr/packages` data.

**Tech Stack:** Go (chi router), Vue 3 + TypeScript, SQLite via existing store layer.

**User decisions (already made):**
- Remove the dedicated PR board entirely; the main board becomes the single view for all contexts.
- PR product string is `pr-92:ppg` (no `PR:` prefix in the URL); the backend reconstructs `isv:percona:PR:pr-92:ppg`.
- Use new dedicated backend routes for PR contexts rather than patching the existing products handler.
- The context selector is a `<select>` inside ContextBar; degrades to a plain badge when only one context exists.

---

## Context Model

A context is a plain object:

```typescript
interface Context {
  label: string      // "PPG" | "PR #92 · ppg"
  apiBase: string    // "/api/products/ppg" | "/api/pr/pr-92/ppg"
  prefix: string     // "isv:percona:ppg" | "isv:percona:PR:pr-92:ppg"  (display only)
}
```

The default context is always:
```typescript
{ label: 'PPG', apiBase: '/api/products/ppg', prefix: 'isv:percona:ppg' }
```

PR contexts are derived client-side from the `/api/pr/packages` response. For each package in each PR group, the project path (e.g. `isv:percona:PR:pr-92:ppg:17:containers`) is parsed to extract `pr-92` and the subproject `ppg`. Unique `(prNumber, subproject)` pairs produce one context each, sorted by PR number descending.

---

## Backend Changes

### New routes

```
GET /api/pr/{pr}/{subproject}/{version}/packages
GET /api/pr/{pr}/{subproject}/{version}/events
```

Both handlers build the OBS prefix as:
```
"isv:percona:PR:" + pr + ":" + subproject
```
and delegate to the existing `store.QueryPackages` / `store.QueryEvents` functions. Version filtering remains client-side (same as the PPG product handler). The `{version}` URL segment is accepted and ignored server-side — it is present only to keep the URL shape symmetric with the products routes, which also ignore version server-side.

Event query parameters (`window`, `from`, `to`) are identical to the existing events handler.

### No changes to existing routes

`/api/products/{product}/{version}/packages` and `/api/products/{product}/{version}/events` are unchanged. `/api/pr/packages` is unchanged (still used for context discovery).

---

## Frontend Changes

### `usePackages` and `useEvents` composables

Both composables replace the `product: MaybeRef<string>` argument with `apiBase: MaybeRef<string>`:

```typescript
// before
export function usePackages(product: MaybeRef<string>, version: MaybeRef<string>)
//   fetches: /api/products/${product}/${version}/packages

// after
export function usePackages(apiBase: MaybeRef<string>, version: MaybeRef<string>)
//   fetches: ${apiBase}/${version}/packages
```

This makes the composables context-agnostic — the caller passes the full base path and the composable appends `/{version}/packages` or `/{version}/events`.

### `App.vue`

- Add `selectedContext` ref (default: PPG context).
- Add `contexts` computed from `prGroups`: derive unique `(prNumber, subproject)` pairs → build Context objects → prepend the PPG default.
- Pass `contexts` and `selectedContext` to `ContextBar`; handle `update:context` emit by setting `selectedContext`, resetting `version` to `'17'`, clearing `activeScopes`, and calling `refresh()`.
- Pass `selectedContext.apiBase` (as a computed ref) to `usePackages` and `useEvents`.
- Remove `<PRBoard :groups="prGroups" />` and its import. Keep `usePRPackages` for context discovery.

### `ContextBar.vue`

- New props: `contexts: Context[]`, `selectedContext: Context`.
- New emit: `update:context` (payload: `Context`).
- Replace the hardcoded `<code>isv:percona:ppg</code>` badge with a `<select>`:
  - Styled with `var(--bg-muted)` background, `var(--font-mono)` font, no visible border — matches the existing version tab container visually.
  - When `contexts.length === 1` (no PRs known yet), render a plain `<code>` badge identical to the current one (no dropdown chrome).
  - When `contexts.length > 1`, render the `<select>` with one `<option>` per context.
- Version tabs and scope chips are unchanged.
- The `<code>` display alongside the selector shows `selectedContext.prefix` so the full OBS path is always visible.

### `PRBoard.vue`

Removed from `App.vue`. The file is left in place but unused.

---

## Context Discovery Logic

```typescript
// derived from prGroups in App.vue
const contexts = computed<Context[]>(() => {
  const ppg: Context = { label: 'PPG', apiBase: '/api/products/ppg', prefix: 'isv:percona:ppg' }
  const seen = new Set<string>()
  const prContexts: Context[] = []

  for (const group of prGroups.value) {
    for (const pkg of group.packages) {
      // project: "isv:percona:PR:pr-92:ppg:17:containers"
      const parts = pkg.project.split(':')
      const prIdx = parts.findIndex(p => p.toLowerCase() === 'pr')
      if (prIdx < 0 || prIdx + 2 >= parts.length) continue
      const prSegment = parts[prIdx + 1]          // "pr-92"
      const subproject = parts[prIdx + 2]          // "ppg"
      const key = `${prSegment}:${subproject}`
      if (seen.has(key)) continue
      seen.add(key)
      const prNum = prSegment.replace(/^pr-/i, '') // "92"
      prContexts.push({
        label: `PR #${prNum} · ${subproject}`,
        apiBase: `/api/pr/${prSegment}/${subproject}`,
        prefix: `isv:percona:PR:${prSegment}:${subproject}`,
      })
    }
  }

  // Sort PR contexts by PR number descending (newest first)
  prContexts.sort((a, b) => {
    const na = parseInt(a.apiBase.split('/')[3].replace(/^pr-/i, ''))
    const nb = parseInt(b.apiBase.split('/')[3].replace(/^pr-/i, ''))
    return nb - na
  })

  return [ppg, ...prContexts]
})
```

---

## Dynamic Version Tabs

Version tabs are no longer hardcoded. The available versions are derived from the packages currently loaded for the selected context.

A version segment is any colon-delimited part of a project path that is a bare integer (e.g. `17` in `isv:percona:ppg:17:containers` or `isv:percona:PR:pr-92:ppg:17`). Packages whose project path contains no such segment (common packages like `isv:percona:ppg:common`) contribute no version and are always shown regardless of the selected version tab.

The version segment always appears immediately after the context prefix in the project path. For `isv:percona:ppg` (3 segments), the version is at index 3; for `isv:percona:PR:pr-92:ppg` (5 segments), it is at index 5. Any purely numeric value at that position is a version; anything else (`common`, `containers`, etc.) is not. No whitelist is needed.

```typescript
// derived in App.vue from the loaded packages for the current context
const availableVersions = computed<string[]>(() => {
  const prefixDepth = selectedContext.value.prefix.split(':').length
  const found = new Set<string>()
  for (const pkg of allPackages.value) {
    const seg = pkg.project.split(':')[prefixDepth]
    if (seg && /^\d+$/.test(seg)) found.add(seg)
  }
  // Sort descending (newest first)
  return [...found].sort((a, b) => parseInt(b) - parseInt(a))
})
```

`availableVersions` is passed to `ContextBar` as a prop replacing the hardcoded `VERSIONS` constant. When the selected context changes and `availableVersions` updates, `version` is reset to `availableVersions[0]` (the highest available version). If `availableVersions` is empty (context has only common packages), the version tab row is hidden.

`matchesVersion` in `usePackages` is updated to use `availableVersions` instead of the hardcoded `['16', '17', '18']` constant for the exclusion list. The same positional logic applies: a package belongs to the selected version if the segment at `prefixDepth` in its project path is either absent (common package) or matches the selected version.

---

## Behaviour Details

- **Scope chips**: unchanged. Scope filtering is applied on the `scope` field values (common, version, container, etc.) which are correctly assigned by the poller.
- **Auto-refresh**: unchanged — 5-minute timer calls `refresh()` which re-fetches for the currently selected context.
- **No PRs yet**: when `/api/pr/packages` returns an empty list, `contexts` has only the PPG entry and ContextBar renders a plain badge — identical to the current UI.
