# Multiple OBS instances

**Date:** 2026-09-25
**Status:** Draft — pending user review

## Problem

Packages, containers and tarballs no longer build on a single OBS instance.
The partition is at the **repository** level: a package `P` may build repos
`R1..RN` on instance X and repos `RN+1..RM` on instance Y. The dashboard must
listen to events from every instance, query every instance's API, and show a
single unified view — with each build target labelled by the instance it runs
on.

Today the backend assumes one instance everywhere: one `obs.Client`, one AMQP
consumer, one `obs_root`, full OBS project names as DB keys, and hardcoded
`build.opensuse.org` / `download.opensuse.org` / `registry.opensuse.org`
hosts in ~12 backend and frontend files.

## Decision summary

- **Instances share a subpath, not a root.** Each instance has its own root
  (e.g. `isv:percona` on openSUSE, `percona` on a self-hosted OBS); the same
  logical project is `<root>:<subpath>` on each.
- **Logical project names drop the root entirely.** Everything the backend and
  frontend key on becomes the root-relative subpath (`ppg:17`,
  `PR:pr-92:ppg:staging`). No instance is primary.
- **Repos are disjoint.** Within a logical package a repo name lives on exactly
  one instance. Target identity stays `(repo, arch)`; `instance` is an
  attribute of the target.
- **Every instance has its own RabbitMQ bus** with the standard OBS routing
  keys under a configurable prefix.
- **Fan-out/routing client (`obs.Fleet`).** A single layer owns one client per
  instance, speaks logical names, merges or routes each call. Everything above
  it (working set, task chain, rollup, store, events) keeps treating a package
  as one logical entity.
- **UI:** per-target instance badge, instance-correct links, an instance
  filter, and per-instance breakdowns in Overview/Metrics. All instance UI is
  hidden when only one instance is configured.

## Alternatives considered

- **Separate pipeline per instance, merged at read time** (rows keyed
  `(instance, project, name)`). Rejected: rollup, `Settled`/parking, build
  events, CVE triggers and state durations are all computed per package and
  would have to be recomputed at merge time or become wrong (e.g. "published"
  while half the repos still build elsewhere).
- **Per-instance target slices merged on upsert.** Rejected: concurrent writers
  per logical package need locking/read-modify-write, and in-memory caches
  (`TargetsStable`, `CacheWarm`) become per-slice. More churn than Fleet, no
  gain.
- **Keep a canonical prefix** (a `primary` instance or a `logical_root`
  setting) to avoid a data migration. Rejected in favour of root-free names;
  the one-time migration is cheap and removes an artificial asymmetry.

## 1. Config and instance model

### Config

```yaml
obs_instances:
  - name: openSUSE                 # UI label; slug "opensuse" used in env vars and DB
    root: "isv:percona"
    api_url: "https://api.opensuse.org"
    web_url: "https://build.opensuse.org"
    download_url: "https://download.opensuse.org/repositories"
    registry: "registry.opensuse.org"
    username: ""                   # or OBS_OPENSUSE_USERNAME
    password: ""                   # or OBS_OPENSUSE_PASSWORD
    minute_request_budget: 60
    mq:
      url: "amqps://opensuse:opensuse@rabbit.opensuse.org:5671/"
      exchange: "pubsub"
      routing_prefix: "opensuse.obs"
  - name: Percona
    root: "percona"
    api_url: "https://api.obs.example.com"
    web_url: "https://obs.example.com"
    download_url: "https://download.obs.example.com/repositories"
    registry: "registry.obs.example.com"
    minute_request_budget: 60
    mq:
      url: "amqps://…"
      exchange: "pubsub"
      routing_prefix: "percona.obs"
```

- `slug` = lowercase `name` with non-alphanumerics replaced by `_`. It is the
  instance's stable identity in the DB (`targets_json`, `events.instance`),
  the API, URL state and env vars (`OBS_<SLUG>_USERNAME`,
  `OBS_<SLUG>_PASSWORD`). `name` is display-only.
- `mq.exchange` defaults to `pubsub`; `mq.routing_prefix` defaults to
  `opensuse.obs`.
- **Legacy fallback:** when `obs_instances` is absent, `config.Load`
  synthesizes one instance named `openSUSE` from the existing `obs.*`,
  `mq.url`, `obs_root` keys and their env vars, with today's hardcoded web,
  download and registry hosts. Existing deployments need no config change.
- **Validation** (startup error): at least one instance, unique names and
  slugs, non-empty `root`, non-empty `api_url`, username present.
- `Config.OBSRoot`, `Config.OBS` and `Config.MQ` are replaced by
  `Config.Instances []InstanceConfig` plus `Config.LegacyRoot` (the value of
  `obs_root`, default `isv:percona`, used only by the migration in §3).

### Logical names

Each instance offers two pure functions:

- `inst.ToLogical("percona:ppg:17") → ("ppg:17", true)`; returns `false` for
  projects outside the instance's root (these are ignored everywhere — this
  replaces the consumer's `HasPrefix(root+":")` filter).
- `inst.ToInstance("ppg:17") → "percona:ppg:17"`.

Consequences:

- `obs.Classify(project)`, `obs.ProjectTags(project)` and `obs.PRNumber`
  drop the `root` argument and parse the subpath directly.
- `api/handlers.go` hardcoded prefixes (`"isv:percona:" + product + …`,
  `QueryPackages(db, "isv:percona:PR")`) become root-free (`product + ":" +
  tier`, `"PR"`). Queries that used the root as prefix mean "all packages".
- Frontend: `contexts.ts` prefixes become `ppg:devel`, `ppg:staging`,
  `ppg:releases`, `PR:<seg>:ppg:<tier>`; `App.vue` PR prefix and
  `useRealtimeStream.ts` prefix parsing are updated; `lib/project.ts`
  (`shortProject`) and its call sites are removed — names are already short.
  `useUrlState` strips a leading `isv:percona:` from incoming URL parameters so
  old bookmarks keep working.

### Rate limiting and metrics

Limiter, request metrics and the publish-flags cache are already per
`obs.Client`, so they become per instance. `Interactive(ctx)` still bypasses
the limiter on every instance.

## 2. Fleet client and routing

### Shape

- `obs.Client` stays a single-instance HTTP client; its constructor takes the
  instance's `api_url` and credentials. It receives and returns
  **instance** project names.
- New `obs.Instance` holds the instance config, its `*Client`, root
  translation, URL builders (§3) and health state.
- New `obs.Fleet` holds `[]*Instance` in config order and exposes the method
  set used today by tasks, poller, API handlers, unblocker, trigger and
  metrics/telemetry — all in **logical** names. It translates names at the
  boundary and stamps results with the instance slug.
- `worker.Task.Run`, `obs.NewPoller`, `mq.NewConsumer`, `api.NewRouter`,
  `unblocker.Rebuilder`, `metricsampler` and `telemetry` switch from
  `*obs.Client` to `*obs.Fleet` (or a narrow interface Fleet satisfies).
  Method names are unchanged, so task-chain logic is untouched.
- `PackageBuildState`, `BinaryArtifact` and similar result types gain an
  `Instance string` field.

### Routing state

1. **Membership** — logical project → set of hosting instances. Rebuilt on
   every poller discovery pass (`SearchProjects` per instance, union of
   logical names); updated between passes by MQ `project.create` /
   `project.delete`. For a project not yet in the map, Fleet queries every
   instance; a 404 means "not hosted there", not an error.
2. **Repo owner** — `(logical project, repo)` → instance. Filled by every
   merged build-results call and by the MQ consumer; seeded at startup from
   stored targets (which carry `instance`).

### Routing rules

| Kind | Methods | Behavior |
|---|---|---|
| Merge across hosting instances | `BuildResults`, `PackageBuildResults`, `RepoPublishStates`, `PackageBlockedReasons`, `ProjectBinaryList`, `ProjectRepos`, `SearchProjects` | Call every hosting instance concurrently, concatenate, stamp `Instance`. Repo-keyed maps merge directly (repos are disjoint). |
| Route by repo owner | `PackageBuildReason`, `PackageContainerInfoFilename`, `PackageContainerTags`, `PackageBinaries`, `RepoBinaryVersions`, `BuildLog`, `PackageHistory`, `BuildDepInfo`, `Rebuild`, `ProjectRepoArchs`, `ProjectRepoPackages` | Look up owner; call only it. Unknown owner → one `PackageBuildResults` refresh, then error. |
| First hosting instance wins | `PackageIsContainer`, `PackageVersionResult`, `SourceHistory` | Try hosting instances in config order; return the first success. |
| Composite | `ProjectPublishFlags` | Fetch per instance (cached per client); return flags whose `Publishes(repo)` delegates to the repo owner's flags. |
| Per instance | `EvictPublishFlags`, `MetricsSnapshot`, `LimiterStats`, `RatePerSecond` | Evict on all instances / return per-instance values plus a total. |

### Partial failure

- Merged calls return `(merged, failed []string, err)`; `err` is non-nil only
  when **every** hosting instance failed.
- **Carry-forward:** `BuildStateTask` and the poller build a package's new
  target list from fresh targets of instances that responded **plus the
  previous targets owned by instances that failed**. An outage on Y never
  deletes or resets Y's targets; X's half keeps updating, Y's half freezes at
  its last known state.
- **Garbage collection** (stale packages, deleted projects) only acts on
  evidence from instances that responded in that tick: a target set is
  dropped when its owning instance confirms it is gone; a package/project row
  is deleted only when no instance hosts it any more.
- **Health:** each `Instance` tracks last success, last error and consecutive
  failures across all its API calls, plus MQ connection state. `ok = false`
  after 3 consecutive API failures or MQ disconnected > 2 minutes.

## 3. Event ingestion and data model

### One consumer per instance

- `mq.Consumer` is constructed from one `*obs.Instance` (MQ url, exchange,
  routing prefix) plus the shared Fleet, DB, hub and working set. `main.go`
  starts one per instance, each with independent reconnect backoff.
- Bindings: `<prefix>.package.#`, `<prefix>.repo.published`,
  `<prefix>.project.#`. `handle` strips the prefix and switches on the
  remainder (`package.build_success`, `project.delete`, …).
- `m.Project` is translated with `inst.ToLogical`; out-of-root projects are
  dropped. The rest of `handle` sees logical names only.
- `mergePackageTarget` stamps `Instance` on the target it inserts/updates and
  records the repo owner in Fleet.
- `project.create` / `project.delete` update Fleet membership.
- **Instance-scoped deletes:** `project.delete` and `package.delete` remove
  only that instance's targets from the affected packages; a package row is
  deleted only when no targets remain, a project's rows only when no instance
  hosts it. (Today's `DeletePackagesByProject` / `DeletePackage` would
  otherwise wipe the other instance's half.) Publish flags are evicted on that
  instance only.
- `repo.published` is unchanged: `awaitingPublishIn(pkg, repo)` already keys
  on repo, which is disjoint.

### Poller

Discovery runs per instance and rebuilds Fleet membership, then fetches
`BuildResults` per logical project through Fleet (merged, with carry-forward
and instance-aware GC as in §2). Release-project handling is unchanged apart
from logical names.

### Model

- `model.Target.Instance string` — `json:"instance"`, stored inside
  `targets_json` (no new packages column). Rollup, `Settled`, `Parkable`,
  target diffing and backoff continue to key on `(repo, arch)`.
- `model.Event.Instance string` — `json:"instance,omitempty"`, new nullable
  `events.instance` column. Set on every target-level event and on
  project/package create/delete; empty for logical events (CVE scans).
- `target_state_durations`, `cve_scans`, `cve_periods` keep their
  `(project, package, repo, arch)` keys; instance is derivable from repo.

### URL builders

Methods on `obs.Instance`, replacing every hardcoded host in the backend:

- `ProjectURL(project)` → `<web_url>/project/show/<instance project>`
- `PackageURL(project, pkg)` → `<web_url>/package/show/<instance project>/<pkg>`
- `LiveLogURL(project, pkg, repo, arch)`
- `DownloadURL(project, repo)` → `<download_url>/<instance project with ':' → ':/'>/<repo>/`
- `ImageBase(project, repo, name)` → `<registry>/<instance project lowercased, ':' → '/'>/<repo>/<name>`

Removes `obsBase` in `worker/worker.go` and `cve/scanner.go`, the registry
literal in `api/release_artifacts.go`, and `cve.ImageBase`. Event `url`
values are built with the target's instance at write time. The CVE scanner
resolves image refs through the target's instance, so containers built on Y
are pulled from Y's registry.

### Migration

Added to the existing idempotent chain in `store.Open`, in one transaction:

1. Add `events.instance` if missing.
2. In `packages`, `events`, `target_state_durations`, `cve_scans`,
   `cve_periods`: strip `<LegacyRoot>:` from `project` where it has that
   prefix.
3. Set `instance` on every target in `targets_json` that lacks it, and on
   `events.instance` for rows with a non-empty repo, to the slug of the first
   configured instance whose root equals `LegacyRoot` (or the first instance
   if none matches).

Re-running finds no prefixed rows / unstamped targets and is a no-op. Stored
event `url` values stay absolute and remain valid.

## 4. API and frontend

### API

- `GET /api/instances` →
  `[{name, slug, root, web_url, download_url, registry, health: {ok, last_success, last_error, consecutive_failures}}]`.
- SSE message `instance_health`, pushed when an instance's `ok` flips.
- Additive payload fields: `Target.instance`, `Event.instance`, and
  `instance` on release-artifact / binary-list items. No endpoint changes
  shape; rebuild keeps `(project, repo, arch, package)` — Fleet resolves the
  owner.
- `/api/metrics` gains `by_instance` (request counts and rates per instance);
  existing totals unchanged.

### Instance data and links

- `useInstances()` composable: fetches `/api/instances` once, applies
  `instance_health` SSE updates.
- `lib/instances.ts`: `obsProject(inst, project)`, `projectUrl`,
  `packageUrl`, `liveLogUrl`, `downloadUrl`, `registryRef`. Replaces the
  hardcoded hosts in `PackageCard`, `GreenStrip`, `PRBoard`,
  `PackagesSubTab`, `TarballsSubTab`, `lib/tarballs.ts`, `useArtifacts`.
- Package-level links (card "open in OBS", GreenStrip project link): one link
  for a single-instance package; one labelled link per instance otherwise
  (`OBS: openSUSE ↗ · Percona ↗`).

### Labels

- Each target box and artifact row shows a small instance-name badge.
- When the instance's `health.ok` is false the badge turns amber with a
  tooltip "`<name>` unreachable — last update `<age>` ago", marking
  carried-forward targets as stale.
- **Single-instance deployments hide all badges, per-instance links and the
  filter**, so the UI is unchanged from today.

### Filter

- Instance chips beside the existing tag chips on the Builds board and in the
  event log. Multi-select; none selected = all. Persisted in the URL as
  `?instances=<slug>,<slug>`.
- Builds board: a package is shown if it has at least one target on a
  selected instance. Inside the card, targets on unselected instances are
  **dimmed, not hidden**, so rollup and counts stay truthful for the whole
  package.
- Event log: filters on `event.instance`; events with no instance (CVE scans)
  are always shown.

### Overview and metrics

- Overview: a "By instance" card (in the `CategoryBreakdown` style) with
  per-instance target counts by state group — ok, failing, building, blocked.
- Metrics panel: per-instance request rate and health next to the existing
  totals.

## Testing

Go unit tests:

- Root translation both ways, including out-of-root projects.
- Config parsing: multi-instance, legacy fallback, env-var credentials,
  each validation error.
- Fleet against per-instance `httptest` servers: merge + instance stamping,
  repo-owner routing (including unknown-owner refresh), first-wins order,
  composite publish flags, 404-as-not-hosted.
- Carry-forward when one hosting instance fails; `err` only when all fail.
- Instance-aware GC in the poller and instance-scoped deletes in the consumer.
- Consumer with a non-default routing prefix and out-of-root filtering.
- Migration on a fixture DB with prefixed rows and unstamped targets; second
  run is a no-op.
- URL builders for both a colon-rooted and a flat root.

Frontend: type-level tests (existing `*.test-d.ts` style) for
`lib/instances.ts` and the updated `contexts.ts`.

Manual: two-instance `docker-compose` run against staging — badges, filter,
per-instance links, and amber badges after breaking one instance's
credentials; single-instance run shows no instance UI.

## Out of scope

- Repos migrating between instances (repos are disjoint by assumption; a
  move is handled only as delete-on-X + create-on-Y via normal GC).
- Name mappings other than root swap.
- Instances without an AMQP bus.
