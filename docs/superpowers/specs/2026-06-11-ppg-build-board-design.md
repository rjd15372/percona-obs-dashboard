# PPG Build Board — Implementation Design

**Date:** 2026-06-11
**Status:** Approved
**Scope:** Full-stack — Go backend + Vue 3 frontend + Docker Compose

---

## 1. Overview

A failure-first build monitor that rolls up every OBS subproject of a Percona product into one view. The backend subscribes to the public OBS RabbitMQ bus for real-time events, polls OBS periodically for bootstrap and enrichment, persists state in SQLite, and serves a REST API. The frontend is a Vue 3 SPA that faithfully ports the approved HTML mockup (`docs/PPG Build Board.html`).

**Core question the dashboard answers every morning:** "Did everything build, and if not, what broke and why?"

---

## 2. Architecture

Single Go binary handles all backend concerns. Vue SPA is served as static files by the Go binary in production; in development a Vite dev server proxies API calls to the Go backend.

```
rabbit.opensuse.org  ──AMQPS──►  MQ Consumer  ──►  SQLite store  ──►  HTTP API  ──►  Vue SPA
build.opensuse.org   ──HTTP──►   OBS Poller   ──►  SQLite store
```

Docker Compose runs two services in development:
- `backend` — Go binary on port 8080, mounts `./data` for SQLite
- `frontend` — Vite dev server on port 5173, volume-mounts `./frontend` for HMR, proxies `/api` to `backend:8080`

---

## 3. Backend

### 3.1 Package layout

```
backend/
  cmd/obsboard/main.go          — wire dependencies, start goroutines
  internal/
    config/config.go            — read env vars with defaults
    model/types.go              — shared structs (Package, Target, Event, Trigger)
    obs/client.go               — OBS HTTP client (authenticated)
    obs/poller.go               — discovery + reconcile loop
    obs/trigger.go              — infer trigger from _history / _builddepinfo
    mq/consumer.go              — AMQP consumer, filter isv:percona events
    store/db.go                 — SQLite schema + migrations
    store/packages.go           — upsert / query package state
    store/events.go             — append / query rolling event log
    api/server.go               — HTTP router setup
    api/handlers.go             — /packages and /events handlers
```

### 3.2 Configuration (environment variables)

All config is read from environment variables. `.env.example` documents every key.

| Variable | Default | Description |
|---|---|---|
| `OBS_USERNAME` | — | OBS account username |
| `OBS_PASSWORD` | — | OBS account password |
| `OBS_BASE_URL` | `https://build.opensuse.org` | OBS API base URL |
| `MQ_URL` | `amqps://opensuse:opensuse@rabbit.opensuse.org:5671/` | AMQP connection URL |
| `POLL_INTERVAL` | `5m` | How often the poller reconciles OBS state |
| `DB_PATH` | `/data/obsboard.db` | SQLite database file path |
| `EVENT_RETENTION` | `7d` | How long events are kept in the store |
| `HTTP_PORT` | `8080` | Port the HTTP server listens on |
| `FRONTEND_DIR` | `` (unset) | Path to Vue build output to serve as static files; unset in dev |

### 3.3 MQ Consumer (`internal/mq`)

- Connects to `rabbit.opensuse.org:5671` over AMQPS (TLS)
- Declares the `pubsub` exchange passively (topic, already exists)
- Creates an exclusive auto-delete queue and binds with `opensuse.obs.package.#` and `opensuse.obs.repo.published`
- Filters messages by `project` field: only processes events where `project` starts with `isv:percona`
- On `build_success` / `build_fail` / `build_unchanged`: calls `store.UpsertPackageState()` then `obs.InferTrigger()` asynchronously, then `store.AppendEvent()`
- On `repo.published`: calls `store.AppendEvent()` directly
- Reconnects with exponential back-off on connection loss; poller reconciles any missed transitions

### 3.4 OBS Poller (`internal/obs`)

Runs on a configurable tick (default 5 minutes). Each tick:

1. **Discover subprojects** — `GET /source?project=isv:percona` to enumerate projects; classify each into scope tier (`common`, `ppgcommon`, `version`, `container`, `release`) by name pattern
2. **Fetch build results** — `GET /build/<project>/_result` per subproject; read repos/arches from project `_meta`
3. **Diff against store** — compare incoming states against `store.QueryPackages()`; call `store.UpsertPackageState()` for any changed packages and `store.AppendEvent()` for state transitions
4. **Bootstrap** — on first tick, fully populates the store from scratch (no prior state assumed)

### 3.5 Trigger inference (`internal/obs/trigger.go`)

Called asynchronously after a package state change. Attempts in order:

1. `GET /build/<project>/<repo>/<arch>/<pkg>/_history` — inspect `reason` field; if it names a dependency, use that as `what`
2. `GET /build/<project>/<repo>/<arch>/_builddepinfo` — diff dependency versions between current and previous build to produce a "pkg A → B" string
3. If state is `failed`: tail `/_log` for the compile error summary
4. Source history `GET /source/<project>/<pkg>/_history` for source service / commit triggers
5. Fall back to `kind: "unknown"` with the raw `reason` string as `what`

Produces a `Trigger{what, kind, at}` struct. `kind` values: `dependency bump`, `toolchain bump`, `base image`, `service`, `unknown`.

### 3.6 SQLite schema

**`packages` table**

| Column | Type | Notes |
|---|---|---|
| `project` | TEXT | OBS project name |
| `name` | TEXT | Package name |
| `scope` | TEXT | `common` / `ppgcommon` / `version` / `container` / `release` |
| `rollup_state` | TEXT | Worst non-ignored target state |
| `ok_targets` | INTEGER | Count of `succeeded` targets |
| `total_targets` | INTEGER | Count of non-ignored targets |
| `trigger_what` | TEXT | Human-readable trigger string |
| `trigger_kind` | TEXT | Trigger category |
| `trigger_at` | DATETIME | When trigger occurred |
| `targets_json` | TEXT | JSON array of `Target` objects |
| `updated_at` | DATETIME | Last upsert time |

Primary key: `(project, name)`

**`events` table**

| Column | Type | Notes |
|---|---|---|
| `id` | TEXT | `evt_<ulid>` |
| `type` | TEXT | `triggered` / `started` / `succeeded` / `failed` / `unresolvable` / `broken` / `blocked` / `published` |
| `scope` | TEXT | Scope tier |
| `project` | TEXT | OBS project |
| `package` | TEXT | Package name |
| `repo` | TEXT | Nullable — absent for project-level events |
| `arch` | TEXT | Nullable |
| `what` | TEXT | Headline |
| `why` | TEXT | Explanation sentence |
| `url` | TEXT | Deep link to OBS |
| `at` | DATETIME | Event time (indexed for range queries) |

Old events are pruned on each poller tick beyond `EVENT_RETENTION`.

### 3.7 HTTP API

Matches the JSON shapes defined in `docs/PPG Build Board - Backend Spec.md` exactly.

| Endpoint | Description |
|---|---|
| `GET /api/products/:product/:version/packages` | Full package list with targets and triggers |
| `GET /api/products/:product/:version/events?window=24h` | Events within time window |
| `GET /api/products/:product/:version/events?from=…&to=…` | Events within explicit date range |

Static files (Vue build) are served from `./frontend/dist` when `FRONTEND_DIR` is set (production). In development the Vite dev server handles the frontend directly.

---

## 4. Frontend

### 4.1 Stack

- **Vue 3** with `<script setup>` + TypeScript
- **Vite** as build tool and dev server
- **Tailwind CSS** — configured with custom theme tokens that map to the CSS variable system (see §4.3)
- **No additional UI library** — all components are hand-rolled to match the mockup exactly

### 4.2 Component tree

```
App.vue                        — fetches data, 5-min auto-refresh, theme state
  AppHeader.vue                — Percona "P" logo · "PPG Build Board" title · light/dark toggle
  ContextBar.vue               — toolbar card
    (version tabs 17/18/16)
    (OBS root code display)
    (updated timestamp · auto-refresh indicator)
    ScopeChip.vue × 6          — all / common / ppgcommon / version / container / release
  HealthHeader.vue             — X/Y packages built · progress bar · fail breakdown pills
  MainGrid.vue                 — two-column layout (packages | event log)
    FailureBoard.vue           — left column
      PackageCard.vue × N      — sorted by rollup severity
        (scope tag · rollup badge · trigger line)
        (repo × arch target grid, color-coded cells)
        (failing targets list: first 3 shown, remainder collapsed)
      GreenStrip.vue           — "N packages built" collapsed strip
    EventLog.vue               — right column, fixed 440px
      TimeWindowPicker.vue     — 1h / 6h / 24h / 3d / 7d presets + custom From/To date range
      EventRow.vue × N         — bucket headers (Today / Yesterday / Earlier)
                                 glyph badge · scope tag · what · why · target · link
```

### 4.3 Design system

CSS variables are declared in `frontend/src/assets/theme.css` and mirror the mockup exactly:

- **Light theme** (`:root`): `--bg-app: #F6F4F0`, `--bg-card: #FFFFFF`, `--brand-purple: #6E3FF3`, `--ok: #1F9D55`, `--fail: #E5484D`, `--warn: #E08A00`, `--broken: #B0203A`, `--blocked: #8A8594`, `--info: #3B82F6`, plus tint variants for each
- **Dark theme** (`[data-theme="dark"]`): `--bg-app: #0B0912`, `--bg-card: #181426`, `--ok: #42C97E`, `--fail: #FF6166`, `--warn: #F0A52A`, `--broken: #FF5E7A`, `--blocked: #9C97A8`, `--info: #5BA0F0`

`tailwind.config.ts` extends the theme with these token names so Tailwind utilities like `bg-card`, `text-ok`, `border-fail` resolve to the correct CSS variable. Theme switching is handled by toggling `data-theme="dark"` on the root element.

**Font:** Roboto self-hosted (condensed / semi-condensed / normal widths, extracted from mockup), falling back to `-apple-system, Segoe UI, Arial`.

### 4.4 Data fetching

Two composables handle all API communication:

- `usePackages(product, version, scopes)` — fetches `/api/products/:p/:v/packages`, filters by active scope selection client-side, recomputes derived counts
- `useEvents(product, version, window)` — fetches `/api/products/:p/:v/events` with the active time window, handles both preset and custom date range

`App.vue` sets a `setInterval` every 5 minutes to re-fetch both. A manual refresh button in `AppHeader` triggers the same fetch immediately.

### 4.5 State

All UI state lives in `App.vue` and is passed down as props:

| State | Type | Default |
|---|---|---|
| `theme` | `'light' \| 'dark'` | `'light'` |
| `product` | `'16' \| '17' \| '18'` | `'17'` |
| `scopes` | `string[]` | `[]` (all) |
| `windowMode` | `'preset' \| 'custom'` | `'preset'` |
| `windowMin` | `string` | `'1440'` (24h) |
| `customStart` | `string \| null` | `null` |
| `customEnd` | `string \| null` | `null` |

---

## 5. Docker Compose

```
docker-compose.yml             — base config (production-ready)
docker-compose.override.yml   — dev overrides (Vite HMR, volume mounts)
.env.example                   — documents all config variables
```

**Services:**

| Service | Image | Ports | Volumes |
|---|---|---|---|
| `backend` | `./backend` (Go multi-stage) | 8080 | `./data:/data` |
| `frontend` | `./frontend` (node:lts, Vite dev server) | 5173 | `./frontend:/app` |

In production only the `backend` container is needed — the Go binary serves the pre-built Vue files from `frontend/dist` (embedded or mounted).

---

## 6. Open questions (deferred, not blocking)

1. **OBS auth** — `isv:` projects may require an authenticated OBS account. `OBS_USERNAME`/`OBS_PASSWORD` are wired into the config; validate whether anonymous access is sufficient for build results.
2. **Multi-product** — the API is parameterised by `product` + `version` today. Extending to PMM, PXC etc. requires only adding OBS root patterns; no schema changes.
3. **Trigger inference fidelity** — `_builddepinfo` diffing is best-effort. Acceptable to ship `kind: "unknown"` for cases where the cause can't be determined; the dashboard remains useful without it.
4. **Event store on restart** — MQ events missed during downtime are recovered by the first poller tick. Events older than `EVENT_RETENTION` are lost; this is acceptable for an internal morning-check tool.
