# Overview rebuilds: count build completions, not observed build starts

**Date:** 2026-07-21
**Status:** Approved

## Problem

The Overview reports 0 rebuilds in the last 24h after an idle night. Its
unit of "one rebuild" is a `target_state_durations` row entering state
`building` (`QueryBuildingEntries`) — but `building` is transient and
only ever observed by *polling* a package mid-build. OBS's message bus
announces completions (`build_success`/`build_fail`/`build_unchanged`),
never starts, so while the idle gate pauses polling no target is ever
seen `building` and the count reads zero. The MQ-signal gate bypass
(idle mode v3) does not help: the signaled fetch runs at completion,
when the target is already past `building`.

Meanwhile the consumer's `UpsertPackageState` → `recordStateTransitions`
already writes one `finished`/`failed` duration row per completion, at
MQ-event time, polling or not — the data is present; the query looks at
the wrong state.

## Decision summary

- **Count build completions** (approach A, user choice): the rebuild
  unit becomes a row entering `finished` OR `failed`. Ground truth from
  MQ, identical while active or idle, and it fixes a pre-existing
  daytime gap (fast builds that polling never caught in `building`).
  Existing prod rows make recent nights appear retroactively on deploy.
- Rejected: synthesizing `building` rows on MQ completions (fabricated
  observations, double-count bookkeeping); a new completion counter or
  event type (the durations table already records completions).
- Semantics shift, accepted: "rebuilds" now means "builds that
  completed in the window" rather than "builds seen starting".

## Design

### 1. Store — `internal/store/overview.go`

`BuildingEntry` → `BuildCompletion`; `QueryBuildingEntries` →
`QueryBuildCompletions`. Same shape and signature otherwise:

```go
// BuildCompletion is one target entering a build-completion state —
// "finished" (successful or unchanged build) or "failed" — the
// Overview's unit of "one rebuild". The MQ consumer's merge writes
// exactly one such transition per completed build at event time, so the
// count is polling-independent.
type BuildCompletion struct {
	Project string
	Package string
	Repo    string
}

// QueryBuildCompletions returns every target_state_durations row that
// entered "finished" or "failed" within [since, until).
func QueryBuildCompletions(db *sql.DB, since, until time.Time) ([]BuildCompletion, error)
```

SQL: `WHERE state IN ('finished', 'failed') AND entered_at >= ? AND
entered_at < ?`, cutoffs bound as RFC3339Nano UTC strings (existing
convention; the lexicographic sub-second caveat in the current comment
carries over unchanged).

### 2. API — `internal/api/overview.go`

Call-site rename only (both the current-window and previous-window
calls go through the same function). Aggregation rules — rebuilds per
logical project, top package, `previous_window_rebuild_total` — are
untouched. JSON shape unchanged; no frontend changes.

### 3. Counting matrix (why exactly once per build)

- MQ `build_success`/`build_unchanged` on a target not already
  `finished` → merge writes `finished` row → counted. The later
  signaled/polled pass moves it to `succeeded` (closes the row, no new
  completion row).
- MQ `build_fail` on a target not already `failed` → `failed` row →
  counted.
- Worker observes `finished` before the MQ event arrives → the MQ
  merge is a no-change → still exactly one row.
- **Accepted limitation:** `build_fail` on an already-`failed` target
  (rebuilt while idle, failed again, nothing observed in between) is a
  no-change — that repeat failure is not counted until some state
  movement is observed. While active, polling usually catches the
  intermediate `building`, making the re-entry to `failed` count.
- Release projects: MQ is ignored for them and the poller-driven
  release states don't cycle through `finished`; release rebuilds were
  effectively uncounted before and remain so — no behavior change.

## Error handling

None new — same query/scan error propagation as today.

## Testing

- **store**: new/renamed test for `QueryBuildCompletions` — seeds one
  in-window `finished`, one in-window `failed`, one in-window
  `building` (the regression guard: must NOT count), one out-of-window
  `finished`; expects exactly the two completions. RFC3339Nano-seeded
  timestamps.
- **api**: `overview_test.go` seed states flip from `building` to
  `finished`/`failed`; per-project expectations keep their current
  values.
- `go test ./... -count=1 && go build ./...` green.
- Live check after deploy: the 24h rebuild count immediately includes
  the previous night's builds (rows already in the DB).

## Alternatives considered

- Synthesized `building` rows on MQ completion — fabricates
  observation data and needs double-count guards; rejected.
- New completion counter/event type — extra machinery duplicating what
  `target_state_durations` already captures; rejected.
- OBS "build started" MQ events — the bus does not publish build
  starts, so no start-based unit can work while idle.
