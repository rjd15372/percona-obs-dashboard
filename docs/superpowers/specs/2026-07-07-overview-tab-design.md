# Overview Tab — Design

**Date:** 2026-07-07
**Status:** Approved (design)

## Problem

The dashboard shows per-package build state (Board) and deliverables (Artifacts) but no at-a-glance summary of **rebuild activity and CVE exposure across all projects**. The user designed the panel externally (claude.ai designer) and provided an implementation-grade UI spec + reference screenshots.

## Inputs (authoritative, in order)

1. **UI spec**: `~/Downloads/obs-dashboard-overview/overview-panel-spec.md` — tokens, layout, component tree, types, aggregation rules, a11y, acceptance criteria. Where this design doc is silent, that spec governs; where they conflict, this doc governs (it records the newer decisions).
2. **Reference images**: `~/Downloads/obs-dashboard-overview/spec-assets/*.png`.
3. **Approved interactive mockup** (adds the 5th card, the app-chrome placement, and the "everything" project scope): https://claude.ai/code/artifact/5f79d92b-82dc-409e-8ac2-2077db79ffc1

## Key decisions (locked during brainstorming)

- **Rebuild unit**: one rebuild = one `target_state_durations` row entering `state='building'` (per repo×arch target). Chosen over `build_started` events because the durations table is never pruned → the previous-window delta works for **all** windows including 7d (events are pruned at `EVENT_RETENTION=7d`, which would break the 7d delta).
- **Project scope: everything including PRs** — grouped into *logical projects* (see below): dev versions, extras, common trees, releases, and each PR.
- **Realtime**: reuse the existing global `/api/stream` — the frontend debounce-refetches `GET /api/overview` on stream activity. No dedicated `/api/overview/stream` (the UI spec's SSE sketch said "align with the backend"; this is the aligned shape).
- **5th stat card — Most Rebuilt Repo** (added during mockup review): global `GROUP BY repo` over the same window query; `topRepo: {name, count}` added to the snapshot.
- **CVE per-image arch aggregation**: critical/high = **max across archs** (arch scans are the same image content; summing would double-count).
- **Tokens**: reuse the app's existing `theme.css` tokens; add only the missing `--crit`/`--high` (+tints). No parallel token system, no hardcoded hex in components.
- **Mono font**: the app's existing mono stack — no JetBrains Mono webfont bundling (deviation from UI spec §1, flagged and accepted at mockup).
- **No in-page theme toggle** — the app header already owns theming (deviation from the reference header, accepted at mockup).
- **Delivery: a plain branch created from `origin/main`** (not a worktree — user explicitly declined). The user has pushed all local work, so `origin/main` == local `main` and the branch carries the full current codebase (telemetry, caching, extras context, …); the plan anchors against current file contents.

## Backend

### Endpoint

`GET /api/overview?window=24h|48h|7d` (default `24h`; anything else → 400) returning:

```jsonc
{
  "window": "24h",
  "generated_at": "2026-07-07T...Z",
  "previous_window_rebuild_total": 402,
  "top_repo": { "name": "images", "count": 84 },
  "projects": [
    {
      "project": "isv:percona:ppg:17",           // logical project key
      "rebuilds": 142,
      "top_package": { "name": "percona-postgresql", "count": 28 },
      "images": [
        { "name": "percona-distribution-postgresql:17",
          "critical": 2, "high": 6,
          "oldest_open_days": 34,                 // 0 = none open / unknown
          "avg_fix_days": 9 }                     // 0 = no closed episodes yet
      ]
    }
  ]
}
```

Frontend types carry the UI spec §4 shapes (`OverviewSnapshot`, `ProjectOverview`, `ContainerImageCve`) plus `top_repo` — but in **snake_case field names matching this JSON**, following the app's existing `types/api.ts` convention (deviation from the UI spec's camelCase). Accent colors are **frontend** config (UI spec §5, ordinal fallback) — not in the payload.

### Logical-project grouping (one Go function, used for both sections)

Maps a raw OBS project to its row key:

| Raw project | Logical project |
|---|---|
| `isv:percona:ppg:<v>` and `…:<v>:containers:*` | `isv:percona:ppg:<v>` |
| `isv:percona:ppg:<v>:extras` and beneath | `isv:percona:ppg:<v>:extras` |
| `isv:percona:ppg:common*` | `isv:percona:ppg:common` |
| `isv:percona:common*` | `isv:percona:common` |
| `isv:percona:ppg:releases*` | `isv:percona:ppg:releases` |
| `isv:percona:PR:pr-N*` | `isv:percona:PR:pr-N` |

(Any other future subproject of a version project groups into its version row until given special treatment.)

### Aggregation queries (validated against the production DB snapshot)

- **Rebuilds per logical project**: `SELECT project, package, repo FROM target_state_durations WHERE state='building' AND entered_at > cutoff` — grouped in Go by logical project. `previous_window_rebuild_total`: same with the shifted `[2w-ago, 1w-ago)` range.
- **top_package** per logical project and **top_repo** globally: same rows, counted by package / repo (ties → first).
- **CVE images**: `SELECT project, package, arch, critical_count, high_count, cve_since FROM cve_scans` grouped by (project, package): `critical`/`high` = max across archs; `oldest_open_days` = `floor(now − min(cve_since))` over vulnerable archs with non-NULL `cve_since`, else 0 (old scans predating age-tracking have NULL → renders `—`).
- **avg_fix_days** per image: mean of `(clean_since − cve_since)` over that (project, package)'s rows in `cve_periods`, rounded; 0 when none (the table currently has 0 rows everywhere — the metric fills in as fixes happen).
- A logical project is included when it has rebuilds in the window **or** at least one scanned image. Image name rendered as `<package>` (the `:<version>` suffix in the mock is part of the package name where OBS names it so).

### Caching

`overviewCache`: 60s TTL + singleflight, keyed by window — the existing `releaseArtifactsCache` pattern in `internal/api` (copy the shape, do not abstract prematurely).

## Frontend

- **Tab**: `mainTab` gains `'overview'`; `AppHeader` gains the tab pill; `useUrlState` persists `?tab=overview&owin=24h|48h|7d` (window param `owin`).
- **Components** (per UI spec §3): `OverviewPanel.vue` (owns window ref + data), `StatCard` grid (×5: Total Rebuilds + delta pill, Most Rebuilt, Most Rebuilt Repo, Open CVEs, Avg CVE Fix Time), `RebuildBarChart` (plain-div bars, normalized to max, sorted desc), `CveExposureTable` (expandable project rows → per-image rows; expansion keyed by project path, multiple open, survives refetches).
- **`useOverviewData(window)`**: fetch on mount/window change; subscribe to the existing global SSE composable and debounce-refetch (~2s) on any message; all aggregates as pure computeds per UI spec §11; loading skeletons (5 cards / 6 bars / 5 rows) and a small inline error banner per UI spec §12.
- **Styling**: existing tokens + new `--crit`/`--high`(+tints) in `theme.css` (both themes); severity badges, age-color helper (grey <21d / amber 21–44d / red ≥45d / `—` at 0), zero badges muted — UI spec §§9–10 exactly.
- **A11y**: UI spec §13 (aria-pressed window buttons, aria-expanded rows, labeled bar tracks, severity never color-only).

## Testing strategy

- **Backend**: unit tests for the logical-project grouping table; handler test seeding `target_state_durations` + `cve_scans` + `cve_periods` fixtures and asserting a full snapshot (rebuild counts, prev-window total, top package/repo, per-image max-across-arch, oldest-open, avg-fix, project inclusion rule); window validation (400); cache TTL/singleflight (call-count assertion, mirroring the release-artifacts cache tests if present).
- **Frontend**: no JS runner (established) — `vue-tsc` via `npm run build` + manual checklist from the UI spec §14 acceptance criteria.
- **Manual**: run docker-compose, compare against the three reference PNGs and the approved mockup at ~1360px in both themes.

## Out of scope

- Dedicated overview SSE stream.
- Pruning `target_state_durations` (unbounded growth — pre-existing, note for a future follow-up).
- Historical trend charts (the separate statistics-dashboard design remains its own future feature).
- JetBrains Mono webfont bundling.
