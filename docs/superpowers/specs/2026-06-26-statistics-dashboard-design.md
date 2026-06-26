# Statistics Dashboard — Design

**Date:** 2026-06-26
**Status:** Approved (design)

## Problem

The dashboard shows current build/artifact state but has no historical or aggregate view. Users can't see trends (is build health improving?), performance (which targets are slow?), security posture over time (CVE counts/ages, remediation speed), or activity throughput. The data to answer these is already captured (events, build state durations, CVE scans/periods) but isn't surfaced.

## Goal

Add a new **Statistics** tab presenting aggregate metrics across four areas — Build Health, Build Performance, CVE Posture, Activity/Throughput — scoped to a selected context and (optionally) version, over a selectable time range, rendered with charts.

## Scope & key decisions (locked during brainstorming)

- **Four areas:** Build Health, Build Performance, CVE Posture, Activity/Throughput.
- **Scoping:** a **context selector** + an **optional version selector ("All versions" default)** + a **time-range switcher** (7d / 30d / 90d / All, default 30d). Metrics are computed **for the selected context/version only — no cross-context aggregation.**
- **Breakdowns are within-context, by repo·arch** (and by package) — not by context.
- **Charts:** **ECharts** (new runtime dependency), themed to the app's CSS variables for light/dark.
- **Backend:** new **per-context aggregation endpoints** with a **TTL cache** (no recompute per request).
- **Phased implementation:** foundation + Build Health first, then one area per later phase.

**Out of scope:** cross-context/global aggregation; real-time streaming of stats (TTL-cached snapshots are fine); finer cuts not listed below (per-repo-only, hourly heatmaps, etc.) — easy to add later; data-model changes (all metrics derive from existing tables).

## Architecture

### Frontend tab
- `App.vue`: extend `mainTab` to `'board' | 'artifacts' | 'statistics'`; add a `v-else-if="mainTab === 'statistics'"` block rendering a new `StatisticsPanel.vue`.
- `AppHeader.vue`: add a "Statistics" tab pill.
- `useUrlState.ts`: accept `?tab=statistics`; also persist `?statsCtx=`, `?statsVersion=`, `?statsRange=` (kept separate from the board/artifacts state).
- **Selectors:** `StatisticsPanel` holds context + version + range state and renders selectors at the top (reusing the app's existing context list and per-context available-versions logic; version list includes an "All versions" entry, default).

### Backend stats endpoints
- New per-context routes mirroring the existing context routing, parameterized by area and range:
  - `/api/products/{product}/{version}/stats/{area}`
  - `/api/pr/{pr}/{subproject}/{version}/stats/{area}`
  - `/api/releases/ppg/{version}/stats/{area}`
  - where `{area}` ∈ `build-health | performance | cve | activity`, `{version}` accepts the sentinel `all` (skip version filter), and `?range=7d|30d|90d|all`.
- Each handler resolves the **project prefix** from the path (the same prefix the existing per-context handlers use), then runs SQL aggregation over `events`, `target_state_durations`, `cve_scans`, `cve_periods`, `packages`, filtered by prefix (+ version unless `all`) and the range window. Results are small JSON.
- Aggregation helpers are shared across areas; route wiring follows the existing per-context handler pattern.

### Caching (TTL)
- A new `statsCache` mirroring the existing `binaryListCache` (`internal/api/artifact_metadata.go`) and `releaseArtifactsCache` (`internal/api/release_artifacts.go`): a struct with `sync.Mutex`, configurable `ttl`, `entries map[string]statsCacheEntry { data, expires, err }`, and `Get(ctx, key, fetch) (T, error)` returning the cached value when within TTL else calling `fetch` and storing it with `expires = now + ttl`.
- Constructed in `server.go` via `newStatsCache(cfg.StatsCacheTTL)`.
- **Cache key:** `"<prefix>:<version>:<range>:<area>"` (e.g. `"isv:percona:ppg:all:30d:build-health"`).
- **TTL:** configurable (`StatsCacheTTL`), default **60s**. Invalidation is TTL-only (no event-driven busting); staleness is bounded by the TTL.
- Concurrency follows the existing caches (mutex-guarded `Get`); single-flight coalescing is an optional later enhancement.

### Charting (ECharts)
- Add `echarts` to `frontend/package.json`.
- A single thin wrapper `frontend/src/components/charts/EChart.vue` that owns `init`/`setOption`/`resize`/`dispose` and takes an `option` prop.
- A shared theme module mapping ECharts colors (series palette, axis/grid/text/tooltip) to the app's CSS variables (`--ok`, `--fail`, `--warn`, `--broken`, `--info`, `--brand-purple`, `--text-*`, `--border`) for light and dark, re-applied when the app theme toggles.

### Frontend data layer
- `useStatistics(area)` composable: fetches `${apiBase}/${version}/stats/${area}?range=${range}` for the current selection, exposes `data` / `loading` / `error`, and refetches when context, version, or range change.

## Metric set (per selected context/version, within the chosen range)

Breakdowns are within-context, by repo·arch / package.

### Build Health (`build-health`)
- **Stat cards:** current pass rate (% succeeded/published of total); # failing/broken now; # packages tracked.
- **Build outcomes over time** — stacked area (succeeded / failed / broken per day), from `events`.
- **Top failing packages, stacked by repo·arch** — horizontal stacked bar; top-N packages by total failures, each bar segmented by repo·arch; from `events` (`GROUP BY package, repo, arch`).
- **Pass rate by repo·arch** — bar.

### Build Performance (`performance`) — all breakdowns by repo·arch
- **Stat cards (overall headline):** p50 & p90 build duration; median rebuilds/day.
- **Duration percentiles by repo·arch** — grouped bar (p50 & p90 per repo·arch), from `target_state_durations`.
- **Duration trend over time** — line chart with a **repo·arch selector** whose default option is **"All repos & arches" (aggregate p50/p90 within the context)**, plus one entry per repo·arch combo to focus the trend on a single target.
- **Slowest packages, stacked by repo·arch** — horizontal stacked bar by median duration.

### CVE Posture (`cve`)
- **Stat cards:** images currently vulnerable; total critical; total high (latest scan per image).
- **CVE count trend over time** — line (critical & high), from `cve_scans`.
- **CVE age distribution** — histogram of how long currently-open CVEs have been open, from `cve_since`.
- **Mean time-to-remediation** — stat card, from `cve_periods` (`cve_since`→`clean_since`).
- **CVE counts by repo·arch** — bar.

### Activity / Throughput (`activity`)
- **Stat cards:** total builds in range; total events; publishes (release velocity).
- **Event volume over time by type** — stacked area, from `events`.
- **Builds per day** — bar.
- **Busiest packages** — bar by event count (within the context).

## Layout

`StatisticsPanel` renders: a top control row (context selector, version selector with "All versions" default, range switcher 7d/30d/90d/All); then one section per area, stacked vertically. Each section = a row of stat cards + a responsive grid of charts. On phone/tablet the card rows and chart grids collapse to one column (same responsive approach as the other tabs, using the existing breakpoints / `useMediaQuery`). Phase 1 ships the control row + the Build Health section; later phases append the other three sections.

## Implementation phases

Each phase is an independent plan, vertically sliced (endpoint + tests + section), shippable on its own:

- **Phase 1 — Foundation + Build Health:** the tab + selectors + URL state; `echarts` dep, `EChart.vue` wrapper + theme module; `statsCache`; `useStatistics`; the `build-health` endpoint (+ tests); the Build Health section.
- **Phase 2 — Build Performance:** `performance` endpoint (+ tests) + section (incl. the repo·arch trend selector).
- **Phase 3 — CVE Posture:** `cve` endpoint (+ tests) + section.
- **Phase 4 — Activity:** `activity` endpoint (+ tests) + section.

## Error handling

- Backend: a failed aggregation returns a 500 with a JSON error; the cache stores the error for the TTL window like the existing caches (or per their exact behavior). Empty ranges return zeroed/empty series, not errors.
- Frontend: `useStatistics` exposes `error`; each section shows a compact error/empty state rather than breaking the tab; charts render empty-state placeholders when a series has no data.

## Testing / Verification

- **Backend (Go):** table tests per area following the existing `*_test.go` pattern — seed a temp SQLite store with known `events` / `target_state_durations` / `cve_scans` / `cve_periods` / `packages`, then assert each endpoint's JSON: counts, percentiles, top-N ordering, repo·arch segmentation, range-window filtering, version filter vs `all`, and cache hit/miss within the TTL.
- **Frontend (Vue):** `npm run build` (vue-tsc + vite) green; manual check that the tab loads, the context/version/range controls refetch, charts render in light + dark (ECharts theme switches with the app theme), and the layout collapses to one column on phone.
- **Per phase:** each phase's endpoint has tests and its section renders, so each is independently verifiable.

## Acceptance Criteria (whole dashboard)

- [ ] A "Statistics" tab exists, switchable via the header, persisted in the URL (`?tab=statistics`).
- [ ] The tab has context + version ("All versions" default) + range (7d/30d/90d/All, default 30d) selectors; changing any refetches.
- [ ] All metrics are scoped to the selected context/version; nothing aggregates across contexts.
- [ ] Within-context breakdowns are by repo·arch (Build Health top-failing & pass-rate; all Build Performance charts; CVE counts) or by package (slowest, busiest).
- [ ] Build Performance's duration trend has a repo·arch selector defaulting to "All repos & arches" (aggregate).
- [ ] Charts render via ECharts, themed to the app's light/dark CSS variables, switching with the theme toggle.
- [ ] Stats endpoints are per-context and TTL-cached (`statsCache`, key `<prefix>:<version>:<range>:<area>`, configurable TTL default 60s); aggregations are not recomputed within the TTL.
- [ ] Backend aggregation has table tests; `npm run build` passes.

## Appendix — "context" defined

A **context** is a selectable scope = one OBS project namespace, represented by the frontend `Context` type (`{ label, apiBase, prefix }`). In this app contexts are: a **product** (e.g. PPG, prefix `isv:percona:ppg`), **Releases**, and **each open PR** (e.g. "PR #92 · ppg", prefix `isv:percona:PR:pr-92:ppg`, derived dynamically). Every package/event carries a `project` string; a context's `prefix` selects which packages/events belong to it. The statistics tab computes metrics for the packages/events under the selected context's prefix (optionally narrowed to one version).
